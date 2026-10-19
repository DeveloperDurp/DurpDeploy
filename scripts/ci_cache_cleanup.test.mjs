import assert from 'node:assert/strict';
import test from 'node:test';
import { cleanup, configuration, createApi } from './ci_cache_cleanup.mjs';

const now = Date.parse('2026-10-20T12:00:00Z');
const options = { projectId: 3, projectPath: 'durfy/apps/durpdeploy', now, log() {} };
const repository = { id: 12, project_id: 3, path: `${options.projectPath}/ci-cache/mr-64` };
const merged = { iid: 64, target_project_id: 3, state: 'merged', merged_at: '2026-10-19T11:59:59Z', head_pipeline: { status: 'success' } };

function fixture(repositories = [repository], requests = [merged]) {
  const calls = [];
  let index = 0;
  const api = {
    async list(path) { assert.equal(path, 'registry/repositories'); return repositories; },
    async request(method, path) {
      calls.push({ method, path });
      if (method === 'DELETE') return;
      assert.equal(path, 'merge_requests/64');
      const response = requests[Math.min(index++, requests.length - 1)];
      if (response instanceof Error) throw response;
      return response;
    },
  };
  return { api, calls };
}

test('dry-run lists eligible repositories without issuing a DELETE', async () => {
  const { api, calls } = fixture();
  assert.equal((await cleanup(api, options))[0].action, 'WOULD_DELETE');
  assert.deepEqual(calls, [{ method: 'GET', path: 'merge_requests/64' }]);
});

test('apply rechecks the MR and requests deletion of only its cache repository', async () => {
  const { api, calls } = fixture();
  assert.equal((await cleanup(api, { ...options, dryRun: false }))[0].action, 'DELETE_REQUESTED');
  assert.deepEqual(calls, [
    { method: 'GET', path: 'merge_requests/64' },
    { method: 'GET', path: 'merge_requests/64' },
    { method: 'DELETE', path: 'registry/repositories/12' },
  ]);
});

test('closed MRs expire from their closure timestamp', async () => {
  const { api } = fixture([repository], [{ ...merged, state: 'closed', merged_at: null, closed_at: '2026-10-18T12:00:00Z' }]);
  assert.equal((await cleanup(api, options))[0].action, 'WOULD_DELETE');
});

test('open, recently closed, future-dated, and active-pipeline MRs remain', async () => {
  for (const change of [
    { state: 'opened', merged_at: null },
    { merged_at: '2026-10-19T12:00:01Z' },
    { merged_at: '2026-10-21T12:00:00Z' },
    ...['running', 'pending', 'created', 'manual', 'waiting_for_resource'].map(status => ({ head_pipeline: { status } })),
  ]) {
    const { api, calls } = fixture([repository], [{ ...merged, ...change }]);
    assert.equal((await cleanup(api, { ...options, dryRun: false }))[0].action, 'KEEP');
    assert.equal(calls.length, 1);
  }
});

test('MR reopening or a new active pipeline during recheck prevents deletion', async () => {
  for (const change of [{ state: 'opened' }, { head_pipeline: { status: 'running' } }, { merged_at: '2026-10-20T11:00:00Z' }]) {
    const { api, calls } = fixture([repository], [merged, { ...merged, ...change }]);
    assert.equal((await cleanup(api, { ...options, dryRun: false }))[0].action, 'KEEP');
    assert.equal(calls.length, 2);
  }
});

test('unrelated paths, pipeline images, and lookalike MR cache paths are never selected', async () => {
  const paths = [
    'other/project/ci-cache/mr-64', `${options.projectPath}/ci`, `${options.projectPath}/ci-browser`,
    `${repository.path}/nested`, `${repository.path}-extra`, `${options.projectPath}/ci-cache/mr-064`,
    `${options.projectPath}/ci-cache/mr-0`, `${options.projectPath}/ci-cache/mr-1/../mr-64`,
  ];
  const { api, calls } = fixture(paths.map(path => ({ ...repository, path })));
  assert.deepEqual(await cleanup(api, { ...options, dryRun: false }), []);
  assert.deepEqual(calls, []);
});

test('invalid project identity, closure data, or API failure stops cleanup', async () => {
  for (const invalid of [{ ...merged, iid: 65 }, { ...merged, target_project_id: 4 }, { ...merged, merged_at: null }, new Error('HTTP 403')]) {
    const { api, calls } = fixture([repository], [invalid]);
    await assert.rejects(cleanup(api, { ...options, dryRun: false }));
    assert.equal(calls.some(call => call.method === 'DELETE'), false);
  }
  for (const change of [{ id: '12' }, { project_id: 4 }]) {
    const { api, calls } = fixture([{ ...repository, ...change }]);
    await assert.rejects(cleanup(api, { ...options, dryRun: false }));
    assert.deepEqual(calls, []);
  }
});

test('deletion failures stop the job', async () => {
  const { api } = fixture();
  const original = api.request;
  api.request = (method, path) => method === 'DELETE' ? Promise.reject(new Error('HTTP 403')) : original(method, path);
  await assert.rejects(cleanup(api, { ...options, dryRun: false }), /HTTP 403/);
});

test('only explicitly enabled schedules on the protected default branch can apply', () => {
  const env = { GITLAB_CI: 'true', CI_PROJECT_ID: '3', CI_PROJECT_PATH: options.projectPath };
  assert.equal(configuration(env).dryRun, true);
  const apply = { ...env, CI_CACHE_CLEANUP_DRY_RUN: 'false', CI_PIPELINE_SOURCE: 'schedule', CI_CACHE_CLEANUP: 'true', CI_COMMIT_REF_PROTECTED: 'true', CI_COMMIT_BRANCH: 'main', CI_DEFAULT_BRANCH: 'main' };
  assert.equal(configuration(apply).dryRun, false);
  for (const change of [
    { GITLAB_CI: 'false' }, { CI_CACHE_CLEANUP_DRY_RUN: 'FALSE' },
    { CI_PIPELINE_SOURCE: 'merge_request_event' }, { CI_CACHE_CLEANUP: '' },
    { CI_COMMIT_REF_PROTECTED: 'false' }, { CI_COMMIT_BRANCH: 'feature' }, { CI_COMMIT_BRANCH: '' },
  ]) assert.throws(() => configuration({ ...apply, ...change }));
});

test('API requests paginate, use job-token headers, and reject redirects and HTTP failures', async () => {
  const calls = [];
  const api = createApi('https://gitlab.example/api/v4', 3, 'fixture', async (url, init) => {
    calls.push({ url, init });
    return { ok: true, json: async () => url.endsWith('page=1') ? Array(100).fill(repository) : [repository] };
  });
  assert.equal((await api.list('registry/repositories')).length, 101);
  assert.equal(calls[1].url, 'https://gitlab.example/api/v4/projects/3/registry/repositories?per_page=100&page=2');
  assert.equal(calls[0].init.headers['JOB-TOKEN'], 'fixture');
  assert.equal(calls[0].init.redirect, 'error');
  const failed = createApi('https://gitlab.example/api/v4', 3, 'fixture', async () => ({ ok: false, status: 403 }));
  await assert.rejects(failed.request('GET', 'registry/repositories'), /HTTP 403/);
  for (const base of ['http://gitlab.example', 'https://user:password@gitlab.example', 'https://gitlab.example/?token=x']) {
    assert.throws(() => createApi(base, 3, 'fixture'));
  }
});
