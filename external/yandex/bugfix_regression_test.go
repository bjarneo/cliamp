package yandex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlexStringDecodesJSONNullAsEmpty(t *testing.T) {
	var tr track
	if err := json.Unmarshal([]byte(`{"id":"123","realId":null,"title":"Song","available":true}`), &tr); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if tr.RealID != "" {
		t.Fatalf("RealID = %q for JSON null, want empty string", tr.RealID)
	}

	var nullID track
	if err := json.Unmarshal([]byte(`{"id":null,"title":"Missing"}`), &nullID); err != nil {
		t.Fatalf("Unmarshal null id: %v", err)
	}
	if nullID.ID != "" {
		t.Fatalf("ID = %q for JSON null, want empty string", nullID.ID)
	}
}

func TestFullDownloadInfoAppendsFormatQueryWithoutExistingQuestionMark(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"host":"s.example","path":"/p","s":"sig","ts":"1"}`))
	}))
	defer srv.Close()

	origGuard := fullDownloadInfoGuard
	fullDownloadInfoGuard = func(string) error { return nil }
	t.Cleanup(func() { fullDownloadInfoGuard = origGuard })

	c := newClient("tok")
	if _, err := c.fullDownloadInfo(context.Background(), srv.URL+"/get-mp3/abc"); err != nil {
		t.Fatalf("fullDownloadInfo() error = %v", err)
	}
	if gotQuery != "format=json" {
		t.Fatalf("query = %q, want format=json", gotQuery)
	}
}

func TestWaveStateLookupKeyMatchesRealID(t *testing.T) {
	w := &waveState{}
	ts := []track{{ID: "100", RealID: "200", Albums: []album{{ID: 55}}}}
	w.keys = trackKeys(ts)
	w.indexTrackKeys(ts)

	got, ok := w.lookupKey("200")
	if !ok || got != "100:55" {
		t.Fatalf("lookupKey(200) = (%q, %v), want (\"100:55\", true)", got, ok)
	}
}
