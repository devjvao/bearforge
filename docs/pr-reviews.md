# Automated pull request reviews

Bearforge uses [OpenCodeReview](https://github.com/alibaba/open-code-review) to
review GitHub pull requests with GPT-6.1 Sol (`gpt-6.1-sol`). Both OCR's review
effort and the model's reasoning effort are set to `high` in
[the workflow](https://github.com/devjvao/bearforge/blob/main/.github/workflows/ocr-review.yml).

## GitHub Actions configuration

In the repository's **Settings > Secrets and variables > Actions**, configure:

| Type | Name | Value | Required |
| --- | --- | --- | --- |
| Secret | `OCR_LLM_AUTH_TOKEN` | An OpenAI API key with access to `gpt-6.1-sol` and available API credits | Yes |
| Secret | `OCR_APP_PRIVATE_KEY` | Full PEM private key generated for the GitHub App | Yes |
| Variable | `OCR_APP_CLIENT_ID` | The GitHub App's Client ID, from its General settings | Yes |
| Variable | `OCR_LLM_MODEL` | `gpt-6.1-sol` | No; this is the workflow default |
| Variable | `OCR_LLM_URL` | `https://api.openai.com/v1/responses` | No; this is the workflow default |

The OpenAI model and endpoint have defaults; the OpenAI API key and GitHub App
credentials must be configured before reviews can run. GitHub supplies a
read-only `GITHUB_TOKEN` for resolving PR context and checking out trusted base
content. A separate App installation token posts the review comments.

The workflow explicitly selects `openai-responses` and sends
`{"reasoning":{"effort":"high"}}`. GPT-6.1 Sol requires the Responses API for
tool calling, which OCR uses during reviews. Keep the Responses endpoint when
using this model. The action's `llm_reasoning_effort` shortcut sends the Chat
Completions field instead, so this workflow supplies the nested Responses field
through `llm_extra_body`. See the
[OpenAI model documentation](https://developers.openai.com/api/docs/models/gpt-6.1-sol)
and [OCR action inputs](https://github.com/alibaba/open-code-review/blob/178543d92fecb7fcfe2da55ffcdc4486997fa75b/action.yml).

## GitHub App identity

1. Open your personal **Settings > Developer settings > GitHub Apps > New GitHub
   App**, or configure an existing App you own. Choose the review bot's display
   name, for example `Devjvao Code Review`, and use `https://github.com/devjvao`
   as its homepage. Disable **Active** under **Webhook**; this workflow does not
   need a webhook endpoint or event subscriptions.
2. Grant these **Repository permissions**: **Contents: Read-only**, **Pull
   requests: Read and write**, and the automatically included **Metadata:
   Read-only**. Select **Only on this account** under where the App can be
   installed to keep it available for repositories in your account.
3. Create the App, then copy its **Client ID** from General settings into the
   Actions variable `OCR_APP_CLIENT_ID`. Use the Client ID, not the numeric App
   ID or a client secret.
4. Under **Private keys**, generate a key. Store the complete downloaded PEM
   contents, including its header, footer, and newlines, in the Actions secret
   `OCR_APP_PRIVATE_KEY`.
5. Under **Install App**, install it on `devjvao`, select **Only select
   repositories**, and include `bearforge` alongside any other repositories
   that will use the App. App registration alone does not grant access to the
   repository. The same App can serve multiple repositories; this workflow
   requests a token scoped only to Bearforge.

The pinned `actions/create-github-app-token` action creates a token scoped to
this repository with only Contents read and Pull requests write permissions.
It revokes the token at job completion. Installation tokens last up to one hour,
so the workflow's 30-minute limit stays within their lifetime. No installation
ID or personal access token is needed. See
[GitHub's App registration guide](https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app)
and [the token action](https://github.com/actions/create-github-app-token).

New review comments are authored by the App's bot account, typically
`<app-slug>[bot]`. Existing comments retain their original author. The pinned
OCR action cannot reliably recognize custom App comments for inline
deduplication or sticky summary ownership, so this workflow uses
`sticky_summary: 'false'` and `incremental: 'false'`. Each run posts a fresh App
summary and its inline findings; a re-review can repeat findings from an earlier
run. This keeps all existing comments and ensures each new summary is authored
by the configured App.

## Automatic and manual reviews

After this workflow is merged into the default branch, automatic reviews run
only for PRs authored by the repository owner (`devjvao`), when the owner opens
them, pushes new commits, reopens them, or marks them ready for review. Draft PRs
are skipped automatically. Outside contributors' PRs do not start paid reviews.

To approve a review of a contributor's PR, or request a review again, the owner
posts a new PR conversation comment beginning with:

```text
/open-code-review
```

Only the repository owner can request or rerun OCR reviews. Other commenters,
including collaborators, cannot trigger them. Bot comments, regular issues, and
unrelated comments are ignored. Manual requests can review draft PRs, but closed
PRs are skipped. The workflow resolves the PR's current base branch and head
commit for each manual request. Further commits by an outside contributor do not
start another review; the owner must post a new command to approve another run.

Each PR has one active review; a new review request cancels its previous run.
Ordinary comments and outside PR events do not cancel a running review. Findings
appear as inline comments from the configured App, with a fresh summary for
each run. Existing inline comments are preserved. Each run reviews the full PR
diff.

## Approving contributor test CI

The repository's **Settings > Actions > General > Approval for running fork pull
request workflows from contributors** must select **Require approval for all
external contributors**. This is a GitHub repository setting, separate from the
workflow files. It also covers returning contributors, who can bypass a
first-time-contributors-only policy.

The regular [CI workflow](https://github.com/devjvao/bearforge/blob/main/.github/workflows/ci.yml) uses `pull_request`, so
outside fork PR workflow runs wait for approval. Review the proposed changes,
then choose **Awaiting approval > Approve workflows to run** on the PR. GitHub
allows users with repository write access to approve these runs; if more writers
are added, they can approve test CI too. Owner pushes to `main` and the release
workflow's CI checks continue to run automatically.

GitHub's fork approval setting does not gate `pull_request_target`, which OCR
uses. That is why OCR separately requires an owner command for contributor PRs.
Approving test workflows does not start an OCR review, and `/open-code-review`
does not approve test CI. See
[GitHub's approval policy documentation](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository#controlling-changes-from-forks-to-workflows-in-public-repositories)
and [how to approve a workflow run](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/approve-runs-from-forks).

## Validation and maintenance

Once the credentials are configured and the workflow is merged, open a small
owner PR and check **Actions > OpenCodeReview PR Review**. Confirm that its review appears,
that both its summary and inline comments show the App's identity, then post
`/open-code-review` and confirm a second run targets the current PR.
For an outside PR, confirm test CI waits for approval and OCR skips automatically;
approve the test workflows and post the owner command to run each separately.
Raw review output and logs are uploaded as workflow artifacts by the OCR action.
Reviews consume OpenAI API tokens, including when manually requested again.

The workflow pins the action to an immutable commit and the OCR CLI to `1.13.11`.
Update both deliberately, checking the action inputs and Responses support.
It uses `pull_request_target` so fork PRs can be reviewed with the configured
credentials. The action checks out trusted base content and reads PR head blobs
through Git; keep execution of PR code in the separate CI workflow.
