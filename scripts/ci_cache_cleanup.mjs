import { mkdir, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

const graceMs = 24 * 60 * 60 * 1000;
const terminalPipelines = new Set(['success', 'failed', 'canceled', 'skipped']);
const positiveId = value => Number.isSafeInteger(value) && value > 0;

export function createApi(base, projectId, token, fetchRequest = fetch) {
  const url = new URL(base);
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash) {
    throw new Error('The GitLab API requires an HTTPS URL without credentials or query parameters.');
  }
  if (!positiveId(projectId) || !token) throw new Error('A project ID and CI job token are required.');
  const root = `${base.replace(/\/$/, '')}/projects/${projectId}/`;
  async function request(method, path) {
    const response = await fetchRequest(root + path, {
      method, headers: { 'JOB-TOKEN': token }, redirect: 'error',
      signal: AbortSignal.timeout(30000),
    });
    if (!response.ok) throw new Error(`GitLab ${method} ${path} returned HTTP ${response.status}.`);
    if (method === 'DELETE') return;
    return response.json();
  }
  async function list(path) {
    const values = [];
    for (let page = 1; page <= 1000; page++) {
      const batch = await request('GET', `${path}?per_page=100&page=${page}`);
      if (!Array.isArray(batch)) throw new Error('GitLab returned an invalid repository list.');
      values.push(...batch);
      if (batch.length < 100) return values;
    }
    throw new Error('Registry repository pagination exceeded its limit.');
  }
  return { request, list };
}

function keepReason(mr, iid, projectId, now) {
  if (mr.iid !== iid || mr.target_project_id !== projectId) {
    throw new Error(`MR ${iid} does not belong to this project.`);
  }
  if (!['merged', 'closed'].includes(mr.state)) return 'MR is open';
  const ended = Date.parse(mr.state === 'merged' ? mr.merged_at : mr.closed_at);
  if (!Number.isFinite(ended)) throw new Error(`MR ${iid} has no valid closure timestamp.`);
  if (now - ended < graceMs) return '24-hour grace period';
  if (mr.head_pipeline && !terminalPipelines.has(mr.head_pipeline.status)) return 'pipeline is active';
  return null;
}

function cacheIid(repository, projectId, projectPath) {
  const prefix = `${projectPath}/ci-cache/mr-`;
  if (!repository.path?.startsWith(prefix)) return null;
  const suffix = repository.path.slice(prefix.length);
  if (!/^[1-9][0-9]*$/.test(suffix)) return null;
  const iid = Number(suffix);
  if (!positiveId(iid) || !positiveId(repository.id) || repository.project_id !== projectId) {
    throw new Error('A cache repository has an invalid project or identifier.');
  }
  return iid;
}

export async function cleanup(api, { projectId, projectPath, dryRun = true, now = Date.now(), log = console.log }) {
  if (!positiveId(projectId) || !projectPath || !Number.isFinite(now)) throw new Error('Invalid cleanup identity.');
  const repositories = await api.list('registry/repositories');
  const report = [];
  for (const repository of repositories) {
    const iid = cacheIid(repository, projectId, projectPath);
    if (iid === null) continue;
    let reason = keepReason(await api.request('GET', `merge_requests/${iid}`), iid, projectId, now);
    let action = reason ? 'KEEP' : 'WOULD_DELETE';
    if (!reason && !dryRun) {
      // Recheck closure and pipeline state immediately before the asynchronous delete.
      reason = keepReason(await api.request('GET', `merge_requests/${iid}`), iid, projectId, now);
      action = reason ? 'KEEP' : 'DELETE_REQUESTED';
      if (!reason) await api.request('DELETE', `registry/repositories/${repository.id}`);
    }
    const decision = { repository: repository.path, id: repository.id, mr: iid, action, reason };
    report.push(decision);
    log(JSON.stringify(decision));
  }
  return report;
}

export function configuration(env) {
  if (env.GITLAB_CI !== 'true') throw new Error('Run cleanup in a GitLab CI job.');
  const mode = env.CI_CACHE_CLEANUP_DRY_RUN ?? 'true';
  if (!['true', 'false'].includes(mode)) throw new Error('CI_CACHE_CLEANUP_DRY_RUN must be true or false.');
  if (mode === 'false' && !(env.CI_PIPELINE_SOURCE === 'schedule'
    && env.CI_CACHE_CLEANUP === 'true' && env.CI_COMMIT_REF_PROTECTED === 'true'
    && env.CI_COMMIT_BRANCH && env.CI_COMMIT_BRANCH === env.CI_DEFAULT_BRANCH)) {
    throw new Error('Deletion requires a cleanup schedule on the protected default branch.');
  }
  return { projectId: Number(env.CI_PROJECT_ID), projectPath: env.CI_PROJECT_PATH, dryRun: mode === 'true' };
}

async function main() {
  const options = configuration(process.env);
  const api = createApi(process.env.CI_API_V4_URL, options.projectId, process.env.CI_JOB_TOKEN);
  const decisions = await cleanup(api, options);
  await mkdir('artifacts/cache-cleanup', { recursive: true });
  await writeFile('artifacts/cache-cleanup/report.json', JSON.stringify({ dryRun: options.dryRun, decisions }, null, 2));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
