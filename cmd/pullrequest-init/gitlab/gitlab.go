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

package gitlab

import (
	"github.com/hashicorp/go-multierror"

	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tektoncd/pipeline/cmd/pullrequest-init/types"

	gitlab "github.com/xanzy/go-gitlab"
	"go.uber.org/zap"
)

const (
	prFile = "pr.json"
)

var toTekton = map[string]types.StatusCode{
	"pending":   types.Queued,
	"running":   types.Queued,
	"success":   types.Success,
	"failure":   types.Failure,
	"cancelled": types.Error,
}

var toGitlab = map[types.StatusCode]string{
	types.Unknown: "failure",
	types.Success: "success",
	types.Failure: "failure",
	types.Error:   "failure",
	// There's no analog for neutral in Gitlab statuses, so default to success
	// to make this non-blocking.
	types.Neutral:        "success",
	types.Queued:         "pending",
	types.InProgress:     "pending",
	types.Timeout:        "failure",
	types.Canceled:       "cancelled",
	types.ActionRequired: "failure",
}

// Handler handles interactions with the GitHub API.
type Handler struct {
	*gitlab.Client

	project string
	mrNum   int

	Logger *zap.SugaredLogger
}

// NewHandler initializes a new handler for interacting with GitHub
// resources.
func NewHandler(ctx context.Context, logger *zap.SugaredLogger, rawURL string) (*Handler, error) {
	token := strings.TrimSpace(os.Getenv("AUTHTOKEN"))
	client := gitlab.NewClient(nil, token)

	project, mrNum, err := parseGitlabURL(rawURL)
	if err != nil {
		return nil, err
	}

	h := &Handler{
		Client:  client,
		project: project,
		mrNum:   mrNum,
		Logger:  logger,
	}
	return h, nil
}

// parseGitlabURL handles a URL in the format: https://gitlab.com/foo/bar/merge_requests/1
func parseGitlabURL(raw string) (string, int, error) {
	p, err := url.Parse(raw)
	if err != nil {
		return "", 0, err
	}
	// The project name can be multiple /'s deep, so split on / and work from right to left.
	split := strings.Split(p.Path, "/")

	// The PR number should be the last element.
	last := len(split) - 1
	prNum := split[last]
	prInt, err := strconv.Atoi(prNum)
	if err != nil {
		return "", 0, fmt.Errorf("unable to parse pr as number from %s", raw)
	}

	// Next we sanity check that this is a correct url. The next to last element should be "merge_requests"
	if split[last-1] != "merge_requests" {
		return "", 0, fmt.Errorf("invalid gitlab url: %s", raw)
	}

	// Next, we rejoin everything else into the project field.
	project := strings.Join(split[1:last-1], "/")
	return project, prInt, nil
}

func (h *Handler) Download(ctx context.Context, path string) (*types.PullRequest, error) {
	rawPrefix := filepath.Join(path, "gitlab")
	if err := os.MkdirAll(rawPrefix, 0755); err != nil {
		return nil, err
	}

	mr, _, err := h.MergeRequests.GetMergeRequest(h.project, h.mrNum, nil)
	if err != nil {
		return nil, err
	}

	pr, err := h.baseGitlabPullRequest(mr)
	if err != nil {
		return nil, err
	}

	c, err := h.downloadComments(mr)
	if err != nil {
		return nil, err
	}
	pr.Comments = c

	statuses, err := h.getStatuses(ctx, pr.Head.SHA)
	if err != nil {
		return nil, err
	}
	pr.Statuses = statuses

	return pr, nil
}

func (h *Handler) Upload(ctx context.Context, pr *types.PullRequest, manifests map[string]types.Manifest) error {
	h.Logger.Infof("Syncing path: %s to pr %d", pr, h.mrNum)

	var merr error

	if err := h.uploadStatuses(ctx, pr.Head.SHA, pr.Statuses); err != nil {
		merr = multierror.Append(merr, err)
	}

	if err := h.uploadLabels(ctx, pr.Labels, manifests["labels"]); err != nil {
		merr = multierror.Append(merr, err)
	}

	if err := h.uploadComments(ctx, pr.Comments, manifests["comments"]); err != nil {
		merr = multierror.Append(merr, err)
	}

	return merr
}

func (h *Handler) baseGitlabPullRequest(mr *gitlab.MergeRequest) (*types.PullRequest, error) {
	targetProj, _, err := h.Projects.GetProject(mr.TargetProjectID, nil)
	if err != nil {
		return nil, err
	}
	sourceProj, _, err := h.Projects.GetProject(mr.SourceProjectID, nil)
	if err != nil {
		return nil, err
	}

	return &types.PullRequest{
		Type: "gitlab",
		ID:   int64(mr.ID),
		Head: &types.GitReference{
			Repo:   targetProj.WebURL,
			Branch: mr.SourceBranch,
			SHA:    mr.DiffRefs.HeadSha,
		},
		Base: &types.GitReference{
			Repo:   sourceProj.WebURL,
			Branch: mr.TargetBranch,
			SHA:    mr.DiffRefs.BaseSha,
		},
		Labels: gitlabLabels(mr),
	}, nil
}

func gitlabLabels(mr *gitlab.MergeRequest) []*types.Label {
	labels := make([]*types.Label, 0, len(mr.Labels))
	for _, l := range mr.Labels {
		labels = append(labels, &types.Label{
			Text: l,
		})
	}
	return labels
}

func (h *Handler) downloadComments(mr *gitlab.MergeRequest) ([]*types.Comment, error) {
	comments := []*types.Comment{}
	ds, _, err := h.Discussions.ListMergeRequestDiscussions(h.project, h.mrNum, nil)
	if err != nil {
		return nil, err
	}
	for _, d := range ds {
		for _, n := range d.Notes {
			comments = append(comments, &types.Comment{
				Text:   n.Body,
				Author: n.Author.Username,
				ID:     int64(n.ID),
				Raw:    "todo",
			})
		}
	}
	return comments, nil
}

// writeJSON writes an arbitrary interface to the given path.
func writeJSON(path string, i interface{}) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(i)
}

func (h *Handler) getStatuses(ctx context.Context, sha string) ([]*types.Status, error) {
	resp, _, err := h.Commits.GetCommitStatuses(h.project, sha, nil)
	if err != nil {
		return nil, err
	}

	statuses := make([]*types.Status, 0, len(resp))
	for _, s := range resp {
		code, ok := toTekton[s.Status]
		if !ok {
			return nil, fmt.Errorf("unknown GitHub status state: %s", s.Status)
		}
		statuses = append(statuses, &types.Status{
			ID:          s.Name,
			Code:        code,
			Description: s.Description,
			URL:         s.TargetURL,
		})
	}
	return statuses, nil

}

func (h *Handler) uploadStatuses(ctx context.Context, sha string, statuses []*types.Status) error {
	var merr error

	for _, s := range statuses {
		state, ok := toGitlab[s.Code]
		if !ok {
			merr = multierror.Append(merr, fmt.Errorf("unknown status code %s", s.Code))
			continue
		}

		if _, _, err := h.Client.Commits.SetCommitStatus(h.project, sha, &gitlab.SetCommitStatusOptions{
			State:       gitlab.BuildStateValue(state),
			Description: &s.Description,
			TargetURL:   &s.URL,
			Context:     &s.ID,
		}); err != nil {
			h.Logger.Warnf("error setting commit status: %s", err)
			merr = multierror.Append(merr, err)
			continue
		}
	}

	return merr
}

func (h *Handler) uploadLabels(ctx context.Context, labels []*types.Label, manifest types.Manifest) error {
	mr, _, err := h.MergeRequests.GetMergeRequest(h.project, h.mrNum, nil)
	if err != nil {
		return err
	}
	for _, l := range mr.Labels {
		if _, ok := manifest[l]; !ok {
			h.Logger.Infof("Not tracking label %s", l)
			labels = append(labels, &types.Label{Text: l})
		}
	}

	labelNames := make([]string, 0, len(labels))
	for _, l := range labels {
		labelNames = append(labelNames, l.Text)
	}
	h.Logger.Infof("Setting labels for PR %d to %v", h.mrNum, labelNames)

	// This field is labeled as omitempty in json, so the request will fail if we don't have any labels to set.
	if len(labelNames) > 0 {
		if _, _, err := h.MergeRequests.UpdateMergeRequest(h.project, h.mrNum, &gitlab.UpdateMergeRequestOptions{
			Labels: gitlab.Labels(labelNames),
		}); err != nil {
			h.Logger.Warnf("error updating PR labels: %s", err)
			return err
		}
	}
	return nil
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

func (h *Handler) uploadComments(ctx context.Context, comments []*types.Comment, manifest types.Manifest) error {
	h.Logger.Infof("Setting comments for PR %d to: %v", h.mrNum, comments)

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
	if err := h.updateExistingComments(ctx, existingComments, manifest); err != nil {
		merr = multierror.Append(merr, err)
	}

	if err := h.createNewComments(ctx, newComments); err != nil {
		merr = multierror.Append(merr, err)
	}

	return merr
}

func (h *Handler) updateExistingComments(ctx context.Context, comments map[int64]*types.Comment, manifest types.Manifest) error {
	existingDiscussions, _, err := h.Discussions.ListMergeRequestDiscussions(h.project, h.mrNum, nil)
	if err != nil {
		return err
	}

	h.Logger.Info(existingDiscussions)
	h.Logger.Info(comments)

	var merr error
	for _, ed := range existingDiscussions {
		for _, ec := range ed.Notes {
			// Check to make sure we were aware of the comment when we started.
			if _, ok := manifest[strconv.FormatInt(int64(ec.ID), 10)]; !ok {
				h.Logger.Infof("Not tracking comment %d. Skipping.", ec.ID)
				continue
			}
			dc, ok := comments[int64(ec.ID)]
			if !ok {
				// Delete
				h.Logger.Infof("Deleting comment %d for PR %d", ec.ID, h.mrNum)
				if _, err := h.Discussions.DeleteMergeRequestDiscussionNote(h.project, h.mrNum, ed.ID, ec.ID, nil); err != nil {
					h.Logger.Warnf("Error deleting comment: %v", err)
					merr = multierror.Append(merr, err)
					continue
				}
			} else if dc.Text != ec.Body {
				// Update

				h.Logger.Infof("Updating comment %d for PR %d to %s", ec.ID, h.mrNum, dc.Text)
				if _, _, err := h.Discussions.UpdateMergeRequestDiscussionNote(h.project, h.mrNum, ed.ID, ec.ID, &gitlab.UpdateMergeRequestDiscussionNoteOptions{
					Body: &dc.Text,
				}); err != nil {
					h.Logger.Warnf("Error editing comment: %v", err)
					merr = multierror.Append(merr, err)
					continue
				}
			}
		}
	}
	return merr
}

func (h *Handler) createNewComments(ctx context.Context, comments []*types.Comment) error {
	var merr error
	for _, dc := range comments {
		h.Logger.Infof("Creating comment %s for PR %d", dc.Text, h.mrNum)
		if _, _, err := h.Discussions.CreateMergeRequestDiscussion(h.project, h.mrNum, &gitlab.CreateMergeRequestDiscussionOptions{
			Body: &dc.Text,
		}); err != nil {
			h.Logger.Warnf("Error creating comment: %v", err)
			merr = multierror.Append(merr, err)
		}

	}
	return merr
}
