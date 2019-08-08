package gitlab

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestGitlab(t *testing.T) {

	fake := NewFakeGitlab()
	s := httptest.NewServer(fake)
	defer s.Close()

	url := "https://www.gitlab.com/dlorenc/foo/merge_requests/1"
	client, _ := NewHandler(context.Background(), zaptest.NewLogger(t, zaptest.WrapOptions(zap.AddCaller())).Sugar(), url)

	client.SetBaseURL(s.URL)
	mr, _, err := client.MergeRequests.GetMergeRequest("project", 3, nil)
	fmt.Println(mr, err)

}
