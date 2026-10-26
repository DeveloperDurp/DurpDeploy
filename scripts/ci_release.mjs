import { execFileSync } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { createApi } from './ci_cache_cleanup.mjs';

export function configuration(env) {
  if (env.CI_PIPELINE_SOURCE !== 'push' || env.CI_COMMIT_REF_PROTECTED !== 'true'
    || !env.CI_COMMIT_BRANCH || env.CI_COMMIT_BRANCH !== env.CI_DEFAULT_BRANCH || env.CI_COMMIT_TAG) {
    throw new Error('Release pages require a push to the protected default branch.');
  }
  if (!/^[0-9a-f]{40}$/.test(env.CI_COMMIT_SHA ?? '')
    || !/^v0\.0\.0-dev\.[1-9][0-9]*\+g[0-9a-f]{12}$/.test(env.software_version ?? '')
    || !env.software_version.endsWith(`+g${env.CI_COMMIT_SHA.slice(0, 12)}`)) {
    throw new Error('The release requires the current commit and its development build version.');
  }
  const project = new URL(env.CI_PROJECT_URL);
  if (project.protocol !== 'https:' || project.username || project.password || project.search || project.hash) {
    throw new Error('The project requires an HTTPS URL without credentials or query parameters.');
  }
  return { sha: env.CI_COMMIT_SHA, tag: env.software_version, projectUrl: project.href.replace(/\/$/, '') };
}

export async function publishRelease(api, options) {
  const { sha, tag, projectUrl, cwd = process.cwd(), outputDir = 'artifacts/release' } = options;
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 }).trim();
  if (git('rev-parse', 'HEAD') !== sha || git('rev-parse', '--is-shallow-repository') !== 'false') {
    throw new Error('Release notes require the full source history at the pipeline commit.');
  }
  const releases = await api.list('releases');
  const existing = releases.find(release => release.tag_name === tag);
  if (existing) {
    if (existing.commit?.id !== sha) throw new Error('The release tag belongs to another commit.');
    return existing;
  }
  const tags = await api.list('repository/tags');
  const existingTag = tags.find(value => value.name === tag);
  if (existingTag && existingTag.commit?.id !== sha) {
    throw new Error('The existing tag belongs to another commit.');
  }
  // Use main's history so a feature-branch release cannot hide unreleased changes.
  const released = new Set(releases.map(release => release.commit?.id));
  const previous = git('rev-list', '--first-parent', sha).split('\n').find(commit => commit !== sha && released.has(commit));
  const range = previous ? `${previous}..${sha}` : sha;
  const markdown = text => text.replace(/[\\`*_{}\[\]()<>!#|]/g, '\\$&');
  const commits = git('log', '--reverse', '--format=%H%x09%s', range).split('\n').map(line => {
    const [commit, ...subject] = line.split('\t');
    return `- [${commit.slice(0, 12)}](${projectUrl}/-/commit/${commit}) ${markdown(subject.join('\t'))}`;
  });
  const comparison = previous
    ? `[Compare changes](${projectUrl}/-/compare/${previous}...${sha}).`
    : 'This first release includes the full source history.';
  const description = `## Changes\n\n${comparison}\n\n${commits.join('\n')}\n`;
  await mkdir(outputDir, { recursive: true });
  await writeFile(`${outputDir}/notes.md`, description);
  const release = await api.request('POST', 'releases', { name: tag, tag_name: tag, ref: sha, description });
  if (release.commit?.id !== sha) throw new Error('GitLab created the release at an unexpected commit.');
  return release;
}

async function main() {
  const options = configuration(process.env);
  const api = createApi(process.env.CI_API_V4_URL, Number(process.env.CI_PROJECT_ID), process.env.CI_JOB_TOKEN);
  const release = await publishRelease(api, options);
  console.log(`Release page: ${release._links?.self ?? `${options.projectUrl}/-/releases/${encodeURIComponent(options.tag)}`}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
