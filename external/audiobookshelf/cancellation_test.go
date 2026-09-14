package audiobookshelf

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestProgressCancellation(t *testing.T) {
	for _, token := range []string{"", "existing-token"} {
		t.Run(token, func(t *testing.T) {
			started := make(chan struct{})
			c := NewClient("https://books.example", token, "user", "password", nil)
			c.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				close(started)
				<-req.Context().Done()
				return nil, req.Context().Err()
			})})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := c.Progress(ctx); done <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("progress lookup did not cancel")
			}
		})
	}
}
