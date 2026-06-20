package status

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestFocusRequest_RoundTrip(t *testing.T) {
	restore := SetFocusDirForTesting(t.TempDir())
	defer restore()

	if err := RequestFocus("T-039"); err != nil {
		t.Fatalf("RequestFocus: %v", err)
	}
	id, ok := TakeFocusRequest()
	if !ok || id != "T-039" {
		t.Fatalf("TakeFocusRequest = (%q, %v), want (T-039, true)", id, ok)
	}
}

func TestFocusRequest_ConsumedExactlyOnce(t *testing.T) {
	restore := SetFocusDirForTesting(t.TempDir())
	defer restore()

	_ = RequestFocus("T-1")
	if _, ok := TakeFocusRequest(); !ok {
		t.Fatal("first take should succeed")
	}
	if id, ok := TakeFocusRequest(); ok {
		t.Errorf("second take should be empty, got (%q, %v)", id, ok)
	}
}

func TestFocusRequest_NoneIsNotOk(t *testing.T) {
	restore := SetFocusDirForTesting(t.TempDir())
	defer restore()

	if id, ok := TakeFocusRequest(); ok {
		t.Errorf("TakeFocusRequest with no request = (%q, %v), want ok=false", id, ok)
	}
}

func TestFocusRequest_LatestWins(t *testing.T) {
	restore := SetFocusDirForTesting(t.TempDir())
	defer restore()

	_ = RequestFocus("T-1")
	_ = RequestFocus("T-2")
	id, ok := TakeFocusRequest()
	if !ok || id != "T-2" {
		t.Errorf("TakeFocusRequest = (%q, %v), want (T-2, true) — latest request wins", id, ok)
	}
}

func TestFocusRequest_StaleDropped(t *testing.T) {
	dir := t.TempDir()
	restore := SetFocusDirForTesting(dir)
	defer restore()

	// Hand-write a marker older than FocusStaleDuration.
	stale := focusRequest{TaskID: "T-old", Updated: time.Now().Add(-2 * FocusStaleDuration).Unix()}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(focusPath(), data, 0644); err != nil {
		t.Fatalf("seeding stale marker: %v", err)
	}

	if id, ok := TakeFocusRequest(); ok {
		t.Errorf("stale request should be dropped, got (%q, %v)", id, ok)
	}
	// ...and removed, so it never fires later.
	if _, err := os.Stat(focusPath()); !os.IsNotExist(err) {
		t.Error("stale marker should have been removed on take")
	}
}

func TestRequestFocus_EmptyTaskIDIsNoOp(t *testing.T) {
	restore := SetFocusDirForTesting(t.TempDir())
	defer restore()

	if err := RequestFocus(""); err != nil {
		t.Fatalf("RequestFocus(\"\") = %v, want nil no-op", err)
	}
	if _, ok := TakeFocusRequest(); ok {
		t.Error("empty RequestFocus should not create a request")
	}
}
