package chat

import (
	"testing"
)

func TestChat_DisplayTitle(t *testing.T) {
	c := Chat{Title: "Sample"}
	if c.DisplayTitle() != "Sample" {
		t.Errorf("DisplayTitle() did not return expected value")
	}
}
