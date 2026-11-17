async function submitReviewVerdict({ github, context, core, fs, env }) {
  const result = JSON.parse(fs.readFileSync('/tmp/ocr-result.json', 'utf8'));
  const manifest = result?.manifest;
  if (env.OCR_EXIT_CODE !== '0'
      || result?.status !== 'complete'
      || manifest?.schema_version !== 'ocr.run-manifest/v1'
      || manifest.operation !== 'review'
      || manifest.terminal_state !== 'complete'
      || manifest.run_failure != null) {
    throw new Error('OCR did not produce a complete review; no verdict submitted.');
  }

  const head = manifest.input?.resolved_head;
  if (!/^[0-9a-f]{40}$/.test(head || '')
      || head !== env.OCR_EXPECTED_HEAD) {
    throw new Error('OCR reviewed an unexpected commit; no verdict submitted.');
  }

  const coverage = manifest.coverage;
  const buckets = ['selected', 'completed', 'reused', 'failed', 'waived'];
  if (!buckets.every(key => Array.isArray(coverage?.[key]))
      || coverage.failed.length || coverage.waived.length) {
    throw new Error('OCR coverage is missing or incomplete; no verdict submitted.');
  }
  const selected = coverage.selected.map(item => item.item_id);
  const reviewed = [...coverage.completed, ...coverage.reused].map(item => item.item_id);
  const selectedIds = new Set(selected);
  if (!selected.every(id => typeof id === 'string' && id.length > 0)
      || selectedIds.size !== selected.length
      || new Set(reviewed).size !== reviewed.length
      || reviewed.length !== selected.length
      || !reviewed.every(id => selectedIds.has(id))) {
    throw new Error('OCR did not review every selected item; no verdict submitted.');
  }

  if (result.comments !== null && !Array.isArray(result.comments)) {
    throw new Error('OCR findings are invalid; no verdict submitted.');
  }
  const count = (result.comments || []).length;
  if (result.summary?.comments !== count
      || env.OCR_COMMENTS_TOTAL !== String(count)
      || env.OCR_COMMENTS_FAILED !== '0') {
    throw new Error('OCR findings were not fully published; no verdict submitted.');
  }
  if (result.warnings != null && !Array.isArray(result.warnings)) {
    throw new Error('OCR warnings are invalid; no verdict submitted.');
  }
  if (selected.length === 0) {
    core.info('OCR selected no files; no verdict submitted.');
    return;
  }
  if (count === 0 && (result.warnings?.length || result.summary.budget_exceeded)) {
    core.warning('OCR reported warnings or exhausted its budget; approval withheld.');
    return;
  }

  const { data: pr } = await github.rest.pulls.get({
    ...context.repo,
    pull_number: context.issue.number
  });
  if (pr.state !== 'open' || pr.draft || pr.head.sha !== head) {
    core.info('PR is closed, a draft, or has new commits; no verdict submitted.');
    return;
  }
  const event = count === 0 ? 'APPROVE' : 'REQUEST_CHANGES';
  await github.rest.pulls.createReview({
    ...context.repo,
    pull_number: context.issue.number,
    commit_id: head,
    event,
    body: count === 0
      ? `OpenCodeReview reviewed all ${selected.length} selected items with no findings.`
      : `OpenCodeReview reported ${count} finding(s). Please address the findings in the inline comments and review summary.`
  });
  core.info(`Submitted ${event} for ${head}.`);
}

module.exports = { submitReviewVerdict };
