package cli

import (
	"fmt"
	"strings"

	"bearforge/internal/app"
	"bearforge/internal/auth"
)

func annotateReassociationAccount(result app.Result, connection auth.Connection) app.Result {
	plan, ok := result.Data.(app.ImportReassociationPlan)
	if !ok {
		return result
	}
	for i := range plan.Changes {
		binding := &plan.Changes[i].Replacement
		if binding.Connection == connection.Name && binding.GrantID == connection.GrantID {
			binding.Account, binding.IdentityVerified = connection.Account, connection.IdentityVerified
		}
	}
	result.Data = plan
	return result
}

func importBindingLabel(binding app.ImportConnectionBinding) string {
	if binding.Connection == "" {
		return "unbound"
	}
	return repositoryListText(binding.Connection) + " (grant " + repositoryListText(binding.GrantID) + ")"
}

// The same review is used for human dry-runs and immediately before the prompt.
// Sanitize even redirected output because connection/account text is external.
func importReassociationReview(plan app.ImportReassociationPlan) string {
	var view strings.Builder
	for _, change := range plan.Changes {
		fmt.Fprintf(&view, "%s (%s)\n  Connection: %s -> %s\n", repositoryListText(change.Repository), repositoryListText(change.Path), importBindingLabel(change.Previous), importBindingLabel(change.Replacement))
		if change.CatalogPrevious != nil {
			fmt.Fprintf(&view, "  Catalog connection: %s -> %s\n", importBindingLabel(*change.CatalogPrevious), importBindingLabel(change.Replacement))
		}
		if change.Replacement.Account != "" && change.Replacement.IdentityVerified {
			fmt.Fprintf(&view, "  Account: %s (verified)\n", repositoryListText(change.Replacement.Account))
		} else {
			fmt.Fprintln(&view, "  Account: identity unverified")
		}
		fmt.Fprintf(&view, "  Origin: %s (retained)\n", repositoryListText(change.Origin))
		if change.GitIntegration == "replace_scoped_helper" {
			fmt.Fprintf(&view, "  Git helper: replace Bearforge routing for %s; reset this URL's helper chain and set useHttpPath=true\n", repositoryListText(change.HelperScope))
			for _, binding := range change.ReviewedBindings {
				fmt.Fprintf(&view, "  Allowed previous helper: %s\n", importBindingLabel(binding))
			}
		} else {
			fmt.Fprintln(&view, "  Git helper: unchanged; SSH uses its existing authentication")
		}
	}
	return view.String()
}

func (o *options) renderImportReassociation(result app.Result) error {
	plan := result.Data.(app.ImportReassociationPlan)
	view := "Authentication repair [" + repositoryListText(result.Outcome) + "]\n" + importReassociationReview(plan)
	if result.OperationID != "" {
		view += "Operation: " + repositoryListText(result.OperationID) + "\n"
	}
	if result.PlanDigest != "" {
		view += "Plan: " + repositoryListText(result.PlanDigest) + "\n"
	}
	if isTerminal(o.out) {
		if _, err := fmt.Fprintln(o.presentation(o.out, true), terminalDocument(view, outputWidth(o.out))); err != nil {
			return err
		}
	} else if _, err := fmt.Fprint(o.out, view); err != nil {
		return err
	}
	if result.Problem != nil {
		return o.diagnostic("Error:", result.Problem.Message)
	}
	return nil
}
