package app

import (
	"context"
	"fmt"

	"github.com/devjvao/bearforge/internal/domain"
)

// RemoteBranchPlan pins the result of the live lookup, including an absent
// branch. Older journals have no lookup and retain their original semantics.
type RemoteBranchPlan struct {
	Remote   string `json:"remote"`
	URL      string `json:"url"`
	OID      string `json:"oid,omitempty"`
	LocalOID string `json:"local_oid,omitempty"`
}

func (s Step) advanceFrom() string {
	if s.RemoteBranch != nil && s.RemoteBranch.LocalOID != s.Start {
		return s.RemoteBranch.LocalOID
	}
	return ""
}

func (s *Service) planWorkspaceBranch(ctx context.Context, bare, remote string, repo domain.Repository, req AddRequest, step *Step) error {
	remoteRef := "refs/remotes/" + remote + "/" + step.Branch
	var remoteOID string
	if !req.Offline && req.Base == "" && req.Tracking != "preserve" {
		oid, err := s.Git.AdvertisedBranch(ctx, bare, remote, step.Branch)
		if err != nil {
			return fmt.Errorf("select a workspace branch (use --offline for cached branch selection): %w", err)
		}
		remoteOID = oid
		step.RemoteBranch = &RemoteBranchPlan{Remote: remote, URL: repo.Remotes[remote], OID: oid}
	} else if step.Create || req.Tracking == "same-name" {
		exists, err := s.Git.RefExists(ctx, bare, remoteRef)
		if err != nil {
			return err
		}
		if exists {
			remoteOID, err = s.Git.Resolve(ctx, bare, remoteRef)
			if err != nil {
				return err
			}
		}
	}
	if req.Tracking == "same-name" && remoteOID == "" {
		return domain.E("conflict", "same-name remote branch does not exist")
	}
	if step.Create {
		if err := s.planNewBranch(ctx, bare, remote, remoteOID, repo.Base, req, step); err != nil {
			return err
		}
	} else {
		oid, err := s.Git.Resolve(ctx, bare, "refs/heads/"+step.Branch)
		if err != nil {
			return err
		}
		step.Start, step.Base, step.Tracking = oid, repo.Base, "preserve"
		if step.RemoteBranch != nil && remoteOID != "" && req.Tracking != "none" {
			err = s.planTrackedLocal(ctx, bare, step)
		} else {
			err = s.checkTracking(ctx, bare, step.Branch, remote, req.Tracking)
		}
		if err != nil {
			return err
		}
	}
	if step.RemoteBranch != nil && step.Start == step.RemoteBranch.OID {
		// Check an advertised commit in preview only when it already exists locally.
		present, err := s.Git.HasCommit(ctx, bare, step.Start)
		if err != nil || !present {
			return err
		}
	}
	return s.checkProvisionCommit(ctx, bare, step.Start)
}

func (s *Service) planNewBranch(ctx context.Context, bare, remote, remoteOID, repoBase string, req AddRequest, step *Step) error {
	step.Tracking = req.Tracking
	if step.Tracking == "preserve" {
		return domain.E("invalid", "preserve tracking requires an existing branch")
	}
	if req.Base == "" && remoteOID != "" {
		step.Base, step.Start = "refs/remotes/"+remote+"/"+step.Branch, remoteOID
		if step.Tracking == "" {
			step.Tracking = "same-name"
		}
	} else {
		step.Base = req.Base
		if step.Base == "" {
			step.Base = repoBase
		}
		var err error
		if step.Base == "" {
			step.Base, err = s.Git.DefaultBase(ctx, bare, remote)
			if err != nil {
				return err
			}
		}
		step.Start, err = s.Git.Resolve(ctx, bare, step.Base)
		if err != nil {
			return err
		}
		if step.Tracking == "" {
			step.Tracking = "none"
		}
	}
	if step.Tracking == "same-name" {
		if step.Start != remoteOID {
			return domain.E("conflict", "base differs from requested tracking branch")
		}
		step.Upstream = remote + "/" + step.Branch
	}
	return nil
}

func (s *Service) planTrackedLocal(ctx context.Context, bare string, step *Step) error {
	pin := step.RemoteBranch
	pin.LocalOID = step.Start
	if err := s.requireMatchingUpstream(ctx, bare, step); err != nil {
		return err
	}
	if pin.LocalOID == pin.OID {
		return nil
	}
	present, err := s.Git.HasCommit(ctx, bare, pin.OID)
	if err != nil {
		return err
	}
	if present {
		ahead, err := s.Git.IsAncestor(ctx, bare, pin.OID, pin.LocalOID)
		if err != nil {
			return err
		}
		if ahead {
			return nil
		}
		behind, err := s.Git.IsAncestor(ctx, bare, pin.LocalOID, pin.OID)
		if err != nil {
			return err
		}
		if !behind {
			return domain.E("conflict", "local %s (%s) and %s/%s (%s) diverged; use --track preserve to keep the local branch or choose another name", step.Branch, pin.LocalOID, pin.Remote, step.Branch, pin.OID)
		}
	}
	// A remote object unavailable during read-only review can only be
	// fast-forwarded after apply fetches it and proves ancestry.
	if err := s.Git.CheckBranchAvailable(ctx, bare, step.Branch, pin.LocalOID); err != nil {
		return err
	}
	step.Start = pin.OID
	step.Base = "refs/remotes/" + pin.Remote + "/" + step.Branch
	return nil
}

func (s *Service) requireMatchingUpstream(ctx context.Context, bare string, step *Step) error {
	remote, err := s.Git.Config(ctx, bare, "branch."+step.Branch+".remote")
	if err != nil {
		return err
	}
	merge, err := s.Git.Config(ctx, bare, "branch."+step.Branch+".merge")
	if err != nil {
		return err
	}
	pin := step.RemoteBranch
	if remote != pin.Remote || merge != "refs/heads/"+step.Branch {
		return domain.E("conflict", "local %s (%s) does not track %s/%s (%s); use --track preserve to keep the local branch or choose another name", step.Branch, pin.LocalOID, pin.Remote, step.Branch, pin.OID)
	}
	return nil
}

func (s *Service) checkProvisionCommit(ctx context.Context, bare, oid string) error {
	submodules, err := s.Git.HasSubmodules(ctx, bare, oid)
	if err != nil {
		return err
	}
	if submodules {
		return domain.E("conflict", "submodules are not supported for provisioning in this milestone")
	}
	return nil
}

func (s *Service) prepareRemoteBranch(ctx context.Context, bare string, repo domain.Repository, step *Step) error {
	pin := step.RemoteBranch
	if repo.Remotes[pin.Remote] != pin.URL {
		return domain.E("conflict", "selected remote changed since planning; review the add plan again")
	}
	oid, err := s.Git.AdvertisedBranch(ctx, bare, pin.Remote, step.Branch)
	if err != nil {
		return err
	}
	if oid != pin.OID {
		return domain.E("conflict", "remote branch changed since planning; review the add plan again")
	}
	if oid != "" {
		if err := s.Git.FetchBranch(ctx, bare, pin.Remote, step.Branch, oid); err != nil {
			return err
		}
	}
	if pin.LocalOID != "" {
		if err := s.requireMatchingUpstream(ctx, bare, step); err != nil {
			return err
		}
	}
	return s.checkProvisionCommit(ctx, bare, step.Start)
}
