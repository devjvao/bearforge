const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

const head = 'a'.repeat(40);
const otherHead = 'b'.repeat(40);
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;

function completeResult() {
  return {
    status: 'complete',
    summary: { comments: 0 },
    comments: null,
    manifest: {
      schema_version: 'ocr.run-manifest/v1',
      operation: 'review',
      terminal_state: 'complete',
      input: { resolved_head: head },
      coverage: {
        selected: [{ item_id: 'first' }, { item_id: 'second' }],
        completed: [{ item_id: 'first' }, { item_id: 'second' }],
        reused: [], failed: [], waived: [],
        excluded: [{ item_id: 'documentation' }],
      },
    },
  };
}

async function runVerdict(result, options = {}) {
  // Execute the actual inline workflow script; no PR code or API calls run.
  const workflow = fs.readFileSync(path.join(__dirname, '../workflows/ocr-review.yml'), 'utf8');
  const step = workflow.split('      - name: Submit review verdict')[1];
  assert.ok(step, 'workflow must submit a review verdict');
  const block = step.match(/          script: \|\r?\n((?: {12}[^\n]*(?:\n|$)|\r?\n)+)/);
  assert.ok(block, 'verdict step must have an inline script');
  const script = block[1].replace(/^ {12}/gm, '');
  const reviews = [];
  const messages = [];
  const github = { rest: { pulls: {
    async get(params) {
      assert.deepEqual(params, { owner: 'owner', repo: 'repository', pull_number: 10 });
      return { data: { state: 'open', draft: false, head: { sha: head }, ...options.pr } };
    },
    async createReview(params) { reviews.push(params); return { data: { html_url: 'https://example.test/review' } }; },
  } } };
  const core = { info: message => messages.push(message), warning: message => messages.push(message) };
  const context = { repo: { owner: 'owner', repo: 'repository' }, issue: { number: 10 } };
  const env = {
    OCR_EXPECTED_HEAD: head,
    OCR_EXIT_CODE: '0',
    OCR_COMMENTS_TOTAL: String(result?.comments?.length || 0),
    OCR_COMMENTS_FAILED: '0',
    ...options.env,
  };
  const readResult = { readFileSync(filename) {
    assert.equal(filename, '/tmp/ocr-result.json');
    if (options.readError) throw options.readError;
    return options.raw ?? JSON.stringify(result);
  } };
  let error;
  try {
    await new AsyncFunction('github', 'context', 'core', 'require', 'process', script)(
      github, context, core, name => { assert.equal(name, 'fs'); return readResult; }, { env },
    );
  } catch (caught) { error = caught; }
  return { reviews, messages, error };
}

test('approves the reviewed commit after complete selected coverage and zero findings', async () => {
  const { reviews, error } = await runVerdict(completeResult());
  assert.ifError(error);
  assert.equal(reviews.length, 1);
  assert.equal(reviews[0].event, 'APPROVE');
  assert.equal(reviews[0].commit_id, head);
  assert.equal(reviews[0].pull_number, 10);
  assert.equal(reviews[0].owner, 'owner');
  assert.equal(reviews[0].repo, 'repository');
});

test('requests changes for any finding, including low severity', async () => {
  const result = completeResult();
  result.comments = [{ severity: 'low', comment: 'A reported issue.' }];
  result.summary.comments = 1;
  const { reviews, error } = await runVerdict(result);
  assert.ifError(error);
  assert.equal(reviews.length, 1);
  assert.equal(reviews[0].event, 'REQUEST_CHANGES');
  assert.equal(reviews[0].commit_id, head);
  assert.ok(reviews[0].body.length > 0);
});

test('accepts reused coverage and an empty comments array', async () => {
  const result = completeResult();
  result.comments = [];
  result.manifest.coverage.reused = [result.manifest.coverage.completed.pop()];
  const { reviews, error } = await runVerdict(result);
  assert.ifError(error);
  assert.equal(reviews[0]?.event, 'APPROVE');
});

const invalidResults = {
  'failed result': result => { result.status = 'failed'; },
  'partial coverage despite a successful action': result => { result.manifest.terminal_state = 'partial'; },
  'failed selected item': result => { result.manifest.coverage.failed = [{ item_id: 'first' }]; },
  'waived selected item': result => { result.manifest.coverage.waived = [{ item_id: 'first' }]; },
  'missing completion': result => { result.manifest.coverage.completed.pop(); },
  'duplicate completions hiding missing coverage': result => { result.manifest.coverage.completed[1] = { item_id: 'first' }; },
  'unexpected completed item': result => { result.manifest.coverage.completed[1] = { item_id: 'unselected' }; },
  'missing selected identity': result => { delete result.manifest.coverage.selected[0].item_id; },
  'missing coverage bucket': result => { delete result.manifest.coverage.failed; },
  'missing manifest': result => { delete result.manifest; },
  'unknown manifest schema': result => { result.manifest.schema_version = 'future'; },
  'non-review operation': result => { result.manifest.operation = 'preview'; },
  'reported run failure': result => { result.manifest.run_failure = { message: 'failed' }; },
  'unexpected reviewed commit': result => { result.manifest.input.resolved_head = otherHead; },
  'malformed findings': result => { result.comments = {}; },
  'inconsistent finding count': result => { result.summary.comments = 1; },
  'invalid warnings': result => { result.warnings = 'truncated'; },
};

for (const [name, change] of Object.entries(invalidResults)) {
  test(`submits no verdict for ${name}`, async () => {
    const result = completeResult();
    change(result);
    const { reviews, error } = await runVerdict(result);
    assert.equal(reviews.length, 0);
    assert.ok(error, 'untrustworthy results must fail the verdict step');
  });
}

for (const [name, change] of Object.entries({
  'no selected files': result => { result.manifest.coverage.selected = []; result.manifest.coverage.completed = []; },
  'review limit warning': result => { result.warnings = [{ code: 'review_limit_reached' }]; },
  'exhausted token budget': result => { result.summary.budget_exceeded = true; },
})) {
  test(`does not approve when there are ${name}`, async () => {
    const result = completeResult();
    change(result);
    const { reviews, messages, error } = await runVerdict(result);
    assert.ifError(error);
    assert.equal(reviews.length, 0);
    assert.ok(messages.length > 0, 'explain why approval was withheld');
  });
}

for (const [name, options] of Object.entries({
  'PR head changed': { pr: { head: { sha: otherHead } } },
  'PR closed': { pr: { state: 'closed' } },
  'PR still a draft': { pr: { draft: true } },
})) {
  test(`submits no verdict when ${name}`, async () => {
    const { reviews, messages, error } = await runVerdict(completeResult(), options);
    assert.ifError(error);
    assert.equal(reviews.length, 0);
    assert.ok(messages.length > 0);
  });
}

for (const [name, options] of Object.entries({
  'nonzero OCR exit': { env: { OCR_EXIT_CODE: '1' } },
  'failed finding publication': { env: { OCR_COMMENTS_FAILED: '1' } },
  'missing publication output': { env: { OCR_COMMENTS_FAILED: '' } },
  'inconsistent publication count': { env: { OCR_COMMENTS_TOTAL: '1' } },
  'missing expected commit': { env: { OCR_EXPECTED_HEAD: '' } },
  'unreadable result': { readError: new Error('ENOENT') },
  'malformed JSON': { raw: '{' },
})) {
  test(`submits no verdict for ${name}`, async () => {
    const { reviews, error } = await runVerdict(completeResult(), options);
    assert.equal(reviews.length, 0);
    assert.ok(error);
  });
}
