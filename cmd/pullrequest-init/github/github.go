/*
Copyright 2019 The Tekton Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tektoncd/pipeline/cmd/pullrequest-init/types"

	"golang.org/x/oauth2"

	"github.com/google/go-github/github"
	"github.com/hashicorp/go-multierror"
	"go.uber.org/zap"
)

const (
	prFile = "pr.json"
)

var (
	toGitHub = map[types.StatusCode]string{
		types.Unknown: "error",
		types.Success: "success",
		types.Failure: "failure",
		types.Error:   "error",
		// There's no analog for neutral in GitHub statuses, so default to success
		// to make this non-blocking.
		types.Neutral:        "success",
		types.Queued:         "pending",
		types.InProgress:     "pending",
		types.Timeout:        "error",
		types.Canceled:       "error",
		types.ActionRequired: "error",
	}
	toTekton = map[string]types.StatusCode{
		"success": types.Success,
		"failure": types.Failure,
		"error":   types.Error,
		"pending": types.Queued,
	}
)

// Handler handles interactions with the GitHub API.
type Handler struct {
	*github.Client

	owner, repo string
	prNum       int

	Logger *zap.SugaredLogger
}

// NewHandler initializes a new handler for interacting with GitHub
// resources.
func NewHandler(ctx context.Context, logger *zap.SugaredLogger, rawURL string) (*Handler, error) {
	token := strings.TrimSpace(os.Getenv("AUTHTOKEN"))
	var hc *http.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		hc = oauth2.NewClient(ctx, ts)
	}

	owner, repo, host, prNumber, err := parseGitHubURL(rawURL)
	if err != nil {
		return nil, err
	}
	var client *github.Client
	if !strings.Contains(host, "github.com") {
		u := fmt.Sprintf("%s/api/v3/", host)
		client, err = github.NewEnterpriseClient(u, u, hc)
		if err != nil {
			return nil, err
		}
	} else {
		client = github.NewClient(hc)
	}
	return &Handler{
		Client: client,
		Logger: logger,
		owner:  owner,
		repo:   repo,
		prNum:  prNumber,
	}, nil
}

// parseURL takes in a raw GitHub URL
// (e.g. https://github.com/owner/repo/pull/1) and extracts the owner, repo, host,
// and pull request number.
func parseGitHubURL(raw string) (string, string, string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", 0, err
	}
	split := strings.Split(u.Path, "/")
	if len(split) < 5 {
		return "", "", "", 0, fmt.Errorf("could not determine PR from URL: %v", raw)
	}
	owner, repo, pr := split[1], split[2], split[4]
	prNumber, err := strconv.Atoi(pr)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("error parsing PR number: %s", pr)
	}

	return owner, repo, u.Scheme + "://" + u.Host, prNumber, nil
}

// writeJSON writes an arbitrary interface to the given path.
func writeJSON(path string, i interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(i)
}

// Download fetches and stores the desired pull request.
func (h *Handler) Download(ctx context.Context, path string) (*types.PullRequest, error) {
	rawPrefix := filepath.Join(path, "github")
	if err := os.MkdirAll(rawPrefix, 0755); err != nil {
		return nil, err
	}

	gpr, _, err := h.PullRequests.Get(ctx, h.owner, h.repo, h.prNum)
	if err != nil {
		return nil, err
	}
	pr := baseGitHubPullRequest(gpr)

	rawStatus := filepath.Join(rawPrefix, "status.json")
	statuses, err := h.getStatuses(ctx, pr.Head.SHA, rawStatus)
	if err != nil {
		return nil, err
	}
	pr.RawStatus = rawStatus
	pr.Statuses = statuses

	rawPR := filepath.Join(rawPrefix, "pr.json")
	if err := writeJSON(rawPR, gpr); err != nil {
		return nil, err
	}
	pr.Raw = rawPR

	// Comments
	pr.Comments, err = h.downloadComments(ctx, rawPrefix)
	if err != nil {
		return nil, err
	}

	return pr, nil
}

func baseGitHubPullRequest(pr *github.PullRequest) *types.PullRequest {
	return &types.PullRequest{
		Type: "github",
		ID:   pr.GetID(),
		Head: &types.GitReference{
			Repo:   pr.GetHead().GetRepo().GetCloneURL(),
			Branch: pr.GetHead().GetRef(),
			SHA:    pr.GetHead().GetSHA(),
		},
		Base: &types.GitReference{
			Repo:   pr.GetBase().GetRepo().GetCloneURL(),
			Branch: pr.GetBase().GetRef(),
			SHA:    pr.GetBase().GetSHA(),
		},
		Labels: githubLabels(pr),
	}
}

func githubLabels(pr *github.PullRequest) []*types.Label {
	labels := make([]*types.Label, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, &types.Label{
			Text: l.GetName(),
		})
	}
	return labels
}

func (h *Handler) downloadComments(ctx context.Context, rawPath string) ([]*types.Comment, error) {
	commentsPrefix := filepath.Join(rawPath, "comments")
	for _, p := range []string{commentsPrefix} {
		if err := os.MkdirAll(p, 0755); err != nil {
			return nil, err
		}
	}
	ic, _, err := h.Issues.ListComments(ctx, h.owner, h.repo, h.prNum, nil)
	if err != nil {
		return nil, err
	}
	comments := make([]*types.Comment, 0, len(ic))
	for _, c := range ic {
		rawComment := filepath.Join(commentsPrefix, fmt.Sprintf("%d.json", c.GetID()))
		h.Logger.Infof("Writing comment %d to file: %s", c.GetID(), rawComment)
		if err := writeJSON(rawComment, c); err != nil {
			return nil, err
		}

		comment := &types.Comment{
			Author: c.GetUser().GetLogin(),
			Text:   c.GetBody(),
			ID:     c.GetID(),

			Raw: rawComment,
		}
		comments = append(comments, comment)
	}
	return comments, nil
}

// readJSON reads an arbitrary JSON payload from path and decodes it into the
// given interface.
func readJSON(path string, i interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return json.NewDecoder(f).Decode(i)
}

// Upload takes files stored on the filesystem and uploads new changes to
// GitHub.
func (h *Handler) Upload(ctx context.Context, pr *types.PullRequest, manifests map[string]types.Manifest) error {
	h.Logger.Infof("Syncing path: %s to pr %d", pr, h.prNum)

	// TODO: Allow syncing from GitHub specific sources.
	var merr error

	if err := h.uploadStatuses(ctx, pr.Head.SHA, pr.Statuses); err != nil {
		merr = multierror.Append(merr, err)
	}

	if err := h.uploadLabels(ctx, manifests["labels"], pr.Labels); err != nil {
		merr = multierror.Append(merr, err)
	}

	if err := h.uploadComments(ctx, manifests["comments"], pr.Comments); err != nil {
		merr = multierror.Append(merr, err)
	}

	return merr
}

func (h *Handler) uploadLabels(ctx context.Context, manifest types.Manifest, raw []*types.Label) error {
	labels := make(map[string]bool)
	for _, l := range raw {
		labels[l.Text] = true
	}

	// Fetch current labels associated to the PR. We'll need to keep track of
	// which labels are new and should not be modified.
	currentLabels, _, err := h.Client.Issues.ListLabelsByIssue(ctx, h.owner, h.repo, h.prNum, nil)
	if err != nil {
		return err
	}
	current := make(map[string]bool)
	for _, l := range currentLabels {
		current[l.GetName()] = true
	}
	h.Logger.Infof("Current labels: %v", current)

	var merr error

	// Create new labels that are missing from the PR.
	create := []string{}
	for l := range labels {
		if !current[l] {
			create = append(create, l)
		}
	}
	h.Logger.Infof("Creating labels %v for PR %d", create, h.prNum)
	if _, _, err := h.Client.Issues.AddLabelsToIssue(ctx, h.owner, h.repo, h.prNum, create); err != nil {
		merr = multierror.Append(merr, err)
	}

	// Remove labels that no longer exist in the workspace and were present in
	// the manifest.
	for l := range current {
		if !labels[l] && manifest[l] {
			h.Logger.Infof("Removing label %s for PR %d", l, h.prNum)
			if _, err := h.Client.Issues.RemoveLabelForIssue(ctx, h.owner, h.repo, h.prNum, l); err != nil {
				merr = multierror.Append(merr, err)
			}
		}
	}

	return err
}

func (h *Handler) uploadComments(ctx context.Context, manifest types.Manifest, comments []*types.Comment) error {
	h.Logger.Infof("Setting comments for PR %d to: %v", h.prNum, comments)

	// Sort comments into whether they are new or existing comments (based on
	// whether there is an ID defined).
	existingComments := map[int64]*types.Comment{}
	newComments := []*types.Comment{}
	for _, c := range comments {
		if c.ID != 0 {
			existingComments[c.ID] = c
		} else {
			newComments = append(newComments, c)
		}
	}

	var merr error
	if err := h.updateExistingComments(ctx, manifest, existingComments); err != nil {
		merr = multierror.Append(merr, err)
	}

	if err := h.createNewComments(ctx, newComments); err != nil {
		merr = multierror.Append(merr, err)
	}

	return merr
}

func (h *Handler) updateExistingComments(ctx context.Context, manifest types.Manifest, comments map[int64]*types.Comment) error {
	currentComments, _, err := h.Issues.ListComments(ctx, h.owner, h.repo, h.prNum, nil)
	if err != nil {
		return err
	}

	var merr error
	for _, ec := range currentComments {
		dc, ok := comments[ec.GetID()]
		if !ok {
			// Current comment does not exist in the current resource.

			// Check if we were aware of the comment when the resource was
			// initialized.
			if _, ok := manifest[strconv.FormatInt(ec.GetID(), 10)]; !ok {
				// Comment did not exist when resource created, so this was created
				// recently. To not modify this comment.
				h.Logger.Infof("Not tracking comment %d. Skipping.", ec.GetID())
				continue
			}

			// Comment existed beforehand, user intentionally deleted. Remove from
			// upstream source.
			h.Logger.Infof("Deleting comment %d for PR %d", ec.GetID(), h.prNum)
			if _, err := h.Issues.DeleteComment(ctx, h.owner, h.repo, ec.GetID()); err != nil {
				h.Logger.Warnf("Error deleting comment: %v", err)
				merr = multierror.Append(merr, err)
				continue
			}
		} else if dc.Text != ec.GetBody() {
			// Update
			c := &github.IssueComment{
				ID:   ec.ID,
				Body: github.String(dc.Text),
				User: ec.User,
			}
			h.Logger.Infof("Updating comment %d for PR %d to %s", ec.GetID(), h.prNum, dc.Text)
			if _, _, err := h.Issues.EditComment(ctx, h.owner, h.repo, ec.GetID(), c); err != nil {
				h.Logger.Warnf("Error editing comment: %v", err)
				merr = multierror.Append(merr, err)
				continue
			}
		}
	}
	return merr
}

func (h *Handler) createNewComments(ctx context.Context, comments []*types.Comment) error {
	var merr error
	for _, dc := range comments {
		c := &github.IssueComment{
			Body: github.String(dc.Text),
		}
		h.Logger.Infof("Creating comment %s for PR %d", dc.Text, h.prNum)
		if _, _, err := h.Issues.CreateComment(ctx, h.owner, h.repo, h.prNum, c); err != nil {
			h.Logger.Warnf("Error creating comment: %v", err)
			merr = multierror.Append(merr, err)
		}
	}
	return merr
}

func (h *Handler) getStatuses(ctx context.Context, sha string, path string) ([]*types.Status, error) {
	resp, _, err := h.Repositories.GetCombinedStatus(ctx, h.owner, h.repo, sha, nil)
	if err != nil {
		return nil, err
	}
	if err := writeJSON(path, resp); err != nil {
		return nil, err
	}

	statuses := make([]*types.Status, 0, len(resp.Statuses))
	for _, s := range resp.Statuses {
		code, ok := toTekton[s.GetState()]
		if !ok {
			return nil, fmt.Errorf("unknown GitHub status state: %s", s.GetState())
		}
		statuses = append(statuses, &types.Status{
			ID:          s.GetContext(),
			Code:        code,
			Description: s.GetDescription(),
			URL:         s.GetTargetURL(),
		})
	}
	return statuses, nil
}

func (h *Handler) uploadStatuses(ctx context.Context, sha string, statuses []*types.Status) error {
	var merr error

	for _, s := range statuses {
		state, ok := toGitHub[s.Code]
		if !ok {
			merr = multierror.Append(merr, fmt.Errorf("unknown status code %s", s.Code))
			continue
		}

		rs := &github.RepoStatus{
			Context:     github.String(s.ID),
			State:       github.String(state),
			Description: github.String(s.Description),
			TargetURL:   github.String(s.URL),
		}
		if _, _, err := h.Client.Repositories.CreateStatus(ctx, h.owner, h.repo, sha, rs); err != nil {
			merr = multierror.Append(merr, err)
			continue
		}
	}

	return merr
}
