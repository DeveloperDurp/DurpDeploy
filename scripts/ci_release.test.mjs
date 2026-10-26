import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { createApi } from './ci_cache_cleanup.mjs';
import { configuration, publishRelease } from './ci_release.mjs';

function fixture(t) {
  const cwd = mkdtempSync(join(tmpdir(), 'release-page-'));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim();
  git('init', '-q', '-b', 'main');
  git('config', 'user.name', 'Release fixture');
  git('config', 'user.email', 'release@example.invalid');
  const commit = subject => {
    git('commit', '-q', '--allow-empty', '-m', subject);
    return git('rev-parse', 'HEAD');
  };
  const first = commit('Initial source');
  const second = commit('Fix [brackets] and `quotes`');
  git('checkout', '-q', '-b', 'feature');
  const feature = commit('Feature change');
  git('checkout', '-q', 'main');
  const third = commit('Main change');
  git('merge', '-q', '--no-ff', 'feature', '-m', 'Merge feature');
  const sha = git('rev-parse', 'HEAD');
  const options = { sha, tag: `v0.0.0-dev.5+g${sha.slice(0, 12)}`, projectUrl: 'https://gitlab.example/group/project', cwd, outputDir: join(cwd, 'notes') };
  const env = {
    CI_PIPELINE_SOURCE: 'push', CI_COMMIT_REF_PROTECTED: 'true', CI_COMMIT_BRANCH: 'main',
    CI_DEFAULT_BRANCH: 'main', CI_COMMIT_SHA: sha, software_version: options.tag, CI_PROJECT_URL: options.projectUrl,
  };
  return { cwd, git, first, second, feature, third, sha, options, env };
}

function apiFixture(releases, sha, failure = 0, tags = []) {
  const calls = [];
  const api = createApi('https://gitlab.example/api/v4', 3, 'fixture-job-token', async (url, init) => {
    calls.push({ url, ...init });
    assert.equal(init.headers['JOB-TOKEN'], 'fixture-job-token');
    assert.equal(init.redirect, 'error');
    if (init.method === 'GET') {
      const page = Number(new URL(url).searchParams.get('page'));
      const values = new URL(url).pathname.endsWith('/repository/tags') ? tags : releases;
      return Response.json(values.slice((page - 1) * 100, page * 100));
    }
    assert.equal(init.method, 'POST');
    assert.equal(url, 'https://gitlab.example/api/v4/projects/3/releases');
    assert.equal(init.headers['Content-Type'], 'application/json');
    if (failure) return Response.json({ message: 'denied' }, { status: failure });
    const payload = JSON.parse(init.body);
    return Response.json({ ...payload, commit: { id: sha } }, { status: 201 });
  });
  return { api, calls };
}

test('first release posts full Git history and retains the exact Markdown notes', async t => {
  const { options, sha, env } = fixture(t);
  assert.deepEqual(configuration(env), { sha, tag: options.tag, projectUrl: options.projectUrl });
  const { api, calls } = apiFixture([], sha);
  const result = await publishRelease(api, options);
  assert.equal(calls.length, 3);
  assert.equal(result.tag_name, options.tag);
  assert.equal(result.ref, sha);
  assert.match(result.description, /first release includes the full source history/);
  assert.match(result.description, /Initial source/);
  assert.match(result.description, /Feature change/);
  assert.match(result.description, /Main change/);
  assert.match(result.description, /Merge feature/);
  assert.ok(result.description.includes('Fix \\[brackets\\] and \\`quotes\\`'));
  assert.equal(result.description, readFileSync(join(options.outputDir, 'notes.md'), 'utf8'));
});

test('nearest released main ancestor wins over timestamps, feature releases, and newer releases', async t => {
  const { options, first, second, feature, third, sha } = fixture(t);
  const releases = [
    { tag_name: 'feature-release', commit: { id: feature } },
    { tag_name: 'older', commit: { id: first } },
    { tag_name: 'previous', commit: { id: second } },
    { tag_name: 'future', commit: { id: 'f'.repeat(40) } },
  ];
  const { api } = apiFixture(releases, sha);
  const { description } = await publishRelease(api, options);
  assert.ok(description.includes(`/-/compare/${second}...${sha}`));
  assert.ok(description.includes(`/-/commit/${third}`));
  assert.ok(description.includes(`/-/commit/${feature}`));
  assert.ok(!description.includes('Initial source'));
  assert.ok(!description.includes('Fix \\[brackets'));
});

test('release lookup paginates before selecting the previous release', async t => {
  const { options, second, sha } = fixture(t);
  const releases = Array.from({ length: 100 }, (_, i) => ({ tag_name: `unrelated-${i}`, commit: { id: 'f'.repeat(40) } }));
  releases.push({ tag_name: 'previous', commit: { id: second } });
  const { api, calls } = apiFixture(releases, sha);
  const result = await publishRelease(api, options);
  assert.equal(calls.length, 4);
  assert.match(calls[1].url, /page=2$/);
  assert.ok(result.description.includes(`/-/compare/${second}...${sha}`));
});

test('job retry preserves an existing release and rejects a conflicting commit', async t => {
  const { options, sha, first } = fixture(t);
  for (const commit of [sha, first]) {
    const existing = { tag_name: options.tag, commit: { id: commit }, description: 'Operator notes' };
    const { api, calls } = apiFixture([existing], sha);
    if (commit === sha) assert.deepEqual(await publishRelease(api, options), existing);
    else await assert.rejects(publishRelease(api, options), /another commit/);
    assert.equal(calls.length, 1);
  }
});

test('bare tag conflicts stop before release creation and matching tags can acquire a release', async t => {
  const { options, sha, first } = fixture(t);
  for (const commit of [first, sha]) {
    const { api, calls } = apiFixture([], sha, 0, [{ name: options.tag, commit: { id: commit } }]);
    if (commit === first) {
      await assert.rejects(publishRelease(api, options), /existing tag belongs to another commit/);
      assert.ok(calls.every(call => call.method === 'GET'));
    } else {
      assert.equal((await publishRelease(api, options)).commit.id, sha);
      assert.equal(calls.filter(call => call.method === 'POST').length, 1);
    }
  }
});

test('publication errors fail the job and preserve notes for inspection', async t => {
  const { options, sha } = fixture(t);
  for (const status of [403, 409, 500]) {
    const { api } = apiFixture([], sha, status);
    await assert.rejects(publishRelease(api, options), new RegExp(`HTTP ${status}`));
    assert.match(readFileSync(join(options.outputDir, 'notes.md'), 'utf8'), /Initial source/);
  }
  const { api } = apiFixture([], 'f'.repeat(40));
  await assert.rejects(publishRelease(api, options), /unexpected commit/);
});

test('wrong checkout and shallow history stop before API access', async t => {
  const { options, sha, first, git } = fixture(t);
  const { api, calls } = apiFixture([], sha);
  await assert.rejects(publishRelease(api, { ...options, sha: first }), /full source history/);
  const shallow = join(options.cwd, 'shallow');
  git('clone', '-q', '--depth=1', `file://${options.cwd}`, shallow);
  await assert.rejects(publishRelease(api, { ...options, cwd: shallow }), /full source history/);
  assert.equal(calls.length, 0);
});

test('only protected default-branch pushes with matching build metadata are authorized', t => {
  const { env } = fixture(t);
  for (const change of [
    { CI_PIPELINE_SOURCE: 'merge_request_event' }, { CI_PIPELINE_SOURCE: 'schedule' }, { CI_PIPELINE_SOURCE: 'web' },
    { CI_COMMIT_REF_PROTECTED: 'false' }, { CI_COMMIT_BRANCH: 'feature' }, { CI_COMMIT_BRANCH: '' },
    { CI_DEFAULT_BRANCH: '' }, { CI_COMMIT_TAG: env.software_version }, { CI_COMMIT_SHA: 'bad' },
    { software_version: 'v1.0.0' }, { software_version: 'v0.0.0-dev.5+gffffffffffff' },
    { CI_PROJECT_URL: 'http://gitlab.example/project' }, { CI_PROJECT_URL: 'https://user:secret@gitlab.example/project' },
  ]) assert.throws(() => configuration({ ...env, ...change }));
});
