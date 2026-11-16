package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/colorprofile"

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

// Dry-runs and confirmation share this allowlisted, width-aware review. Grant
// identities are included because replacing one is the action being reviewed.
func importReassociationDocument(p *colorprofile.Writer, plan app.ImportReassociationPlan, width int) string {
	sections := []string{}
	metadata := [][]string{}
	if plan.OperationID != "" {
		metadata = append(metadata, []string{"Operation", plan.OperationID})
	}
	if plan.Root != "" {
		metadata = append(metadata, []string{"Root", plan.Root})
	}
	if len(metadata) > 0 {
		sections = append(sections, terminalRecords(p, metadata, width))
	}
	for _, change := range plan.Changes {
		sections = append(sections, recoveryOutputHeading(p, "Repository "+change.Repository))
		account := "identity unverified"
		if change.Replacement.Account != "" && change.Replacement.IdentityVerified {
			account = change.Replacement.Account + " (verified)"
		}
		sections = append(sections, terminalRecords(p, [][]string{{"Path", change.Path}, {"Origin", change.Origin + " (retained)"}, {"Account", account}}, width))
		bindings := [][]string{{"Journal", change.Previous.Connection, change.Previous.GrantID}}
		if change.CatalogPrevious != nil {
			bindings = append(bindings, []string{"Catalog", change.CatalogPrevious.Connection, change.CatalogPrevious.GrantID})
		}
		bindings = append(bindings, []string{"Replacement", change.Replacement.Connection, change.Replacement.GrantID})
		sections = append(sections, recoveryOutputHeading(p, "Connection changes"), renderResultRows(p, []string{"SOURCE", "CONNECTION", "GRANT"}, bindings, width))
		if change.GitIntegration == "replace_scoped_helper" {
			sections = append(sections, recoveryOutputHeading(p, "Git integration"), terminalRecords(p, [][]string{
				{"Action", "Replace Bearforge credential routing"}, {"Scope", change.HelperScope}, {"Settings", "Reset this URL's helper chain; useHttpPath=true"},
			}, width))
			previous := [][]string{}
			for _, binding := range change.ReviewedBindings {
				previous = append(previous, []string{binding.Connection, binding.GrantID})
			}
			if len(previous) > 0 {
				sections = append(sections, recoveryOutputHeading(p, "Allowed previous helpers"), renderResultRows(p, []string{"CONNECTION", "GRANT"}, previous, width))
			}
		} else {
			sections = append(sections, recoveryOutputHeading(p, "Git integration"), "Unchanged; SSH uses its existing authentication.")
		}
	}
	return terminalDocument(strings.Join(sections, "\n\n"), width)
}

func (o *options) writeImportReassociationReview(plan app.ImportReassociationPlan) error {
	p := o.presentation(o.errOut, isTerminal(o.errOut))
	_, err := fmt.Fprintln(p, importReassociationDocument(p, plan, outputWidth(o.errOut)))
	return err
}

func (o *options) renderImportReassociation(result app.Result) error {
	plan := result.Data.(app.ImportReassociationPlan)
	p := o.presentation(o.out, isTerminal(o.out))
	if plan.OperationID == "" {
		plan.OperationID = result.OperationID
	}
	sections := []string{recoveryOutputHeading(p, "Authentication repair ["+result.Outcome+"]"), importReassociationDocument(p, plan, outputWidth(o.out))}
	if result.PlanDigest != "" {
		sections = append(sections, terminalRecords(p, [][]string{{"Plan", result.PlanDigest}}, outputWidth(o.out)))
	}
	if _, err := fmt.Fprintln(p, terminalDocument(strings.Join(sections, "\n\n"), outputWidth(o.out))); err != nil {
		return err
	}
	if result.Problem != nil {
		return o.diagnostic("Error:", result.Problem.Message)
	}
	return nil
}
