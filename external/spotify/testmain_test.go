package spotify

import (
	"net/http"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Unsetenv("CLIAMP_CONFIG_DIR")
	os.Unsetenv("XDG_CONFIG_HOME")
	os.Exit(m.Run())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
