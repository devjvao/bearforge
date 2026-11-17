const fs = require('node:fs');
const path = require('node:path');
const { submitReviewVerdict } = require('./ocr-review-verdict.cjs');

const args = process.argv.slice(2);
if (args.length > 1 || (args.length === 1 && args[0] !== '--check')) {
  throw new Error('Usage: node .github/scripts/sync-ocr-review-verdict.cjs [--check]');
}
const workflowPath = path.join(__dirname, '../workflows/ocr-review.yml');
const workflow = fs.readFileSync(workflowPath, 'utf8').replace(/\r\n/g, '\n');
const begin = '            // BEGIN GENERATED OCR VERDICT';
const end = '            // END GENERATED OCR VERDICT';
const first = workflow.indexOf(begin);
const last = workflow.indexOf(end);
if (first < 0 || last < first
    || workflow.indexOf(begin, first + 1) !== -1
    || workflow.indexOf(end, last + 1) !== -1) {
  throw new Error('Expected exactly one ordered pair of OCR verdict bundle markers.');
}

// Serialize the statically imported function into YAML; never evaluate source.
// Keep all dependencies explicit in its arguments so the bundle needs no imports.
const declaration = submitReviewVerdict.toString().replace(/\r\n/g, '\n');
const bundle = [
  begin,
  ...declaration.split('\n').map(line => line ? `            ${line}` : ''),
  '',
  '            await submitReviewVerdict({',
  '              github, context, core, fs: require(\'fs\'), env: process.env',
  '            });',
  end,
].join('\n');
const updated = workflow.slice(0, first) + bundle + workflow.slice(last + end.length);
if (updated === workflow) {
  console.log('OCR verdict bundle is up to date.');
} else if (args[0] === '--check') {
  console.error('OCR verdict bundle is stale. Run node .github/scripts/sync-ocr-review-verdict.cjs');
  process.exitCode = 1;
} else {
  fs.writeFileSync(workflowPath, updated);
  console.log('Updated the inline OCR verdict bundle.');
}
