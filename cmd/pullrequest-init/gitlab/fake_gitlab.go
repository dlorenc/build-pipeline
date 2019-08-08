package gitlab

import (
	"encoding/json"
	"net/http"
	"strconv"

	api "github.com/xanzy/go-gitlab"

	"github.com/gorilla/mux"
)

// key defines keys for associating data to PRs/issues in the fake server.

// FakeGitlab is a fake Gitlab server for use in tests.
type FakeGitlab struct {
	*mux.Router
	mergeRequests map[string]api.MergeRequest
}

// FakeGitlab returns a new FakeGitlab.
func NewFakeGitlab() *FakeGitlab {
	s := &FakeGitlab{
		Router: mux.NewRouter(),
	}
	s.HandleFunc("/api/v4/projects/{project}/merge_requests/{number}", s.getPullRequest).Methods(http.MethodGet)
	s.HandleFunc("/api/v1/projects/{project}", s.getProject).Methods(http.MethodGet)
	// s.HandleFunc("/repos/{owner}/{repo}/issues/{number}/comments", s.createComment).Methods(http.MethodPost)
	// s.HandleFunc("/repos/{owner}/{repo}/issues/comments/{number}", s.updateComment).Methods(http.MethodPatch)
	// s.HandleFunc("/repos/{owner}/{repo}/issues/comments/{number}", s.deleteComment).Methods(http.MethodDelete)
	// s.HandleFunc("/repos/{owner}/{repo}/issues/{number}/labels", s.updateLabels).Methods(http.MethodPut)
	// s.HandleFunc("/repos/{owner}/{repo}/statuses/{revision}", s.createStatus).Methods(http.MethodPost)
	// s.HandleFunc("/repos/{owner}/{repo}/commits/{revision}/status", s.getStatuses).Methods(http.MethodGet)

	return s
}

func (g *FakeGitlab) getPullRequest(w http.ResponseWriter, r *http.Request) {
	num := mux.Vars(r)["number"]
	// mr, ok := g.mergeRequests[num]
	// if !ok {
	// 	w.WriteHeader(http.StatusNotFound)
	// 	return
	// }

	numm, _ := strconv.Atoi(num)
	mr := api.MergeRequest{
		ID: numm,
	}
	write(mr, w)
}

func (g *FakeGitlab) getProject(w http.ResponseWriter, r *http.Request) {
	project := mux.Vars(r)["project"]
	p := api.Project{
		Name: project,
	}
	write(p, w)
}

func write(v interface{}, w http.ResponseWriter) {
	b, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(b)
}
