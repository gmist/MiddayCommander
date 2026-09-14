package menubar

import (
	"strings"
	"testing"

	"github.com/kooler/MiddayCommander/internal/config"
)

// The bar is built from a hardcoded table, so a new binding can silently miss
// it and leave the action invisible.
func TestEveryShiftFKeyBindingIsLabelled(t *testing.T) {
	cfg := config.Default()
	items := ShiftItems(cfg)

	bindings := map[string]config.StringOrList{
		"rename":    cfg.Keys.Rename,
		"copy_path": cfg.Keys.CopyPath,
		"servers":   cfg.Keys.Servers,
	}

	for name, keys := range bindings {
		for _, k := range keys {
			pos := shiftFKeyPos(k)
			if pos < 0 {
				continue // not a shift F-key, nothing to show
			}
			if items[pos].Label == "" {
				t.Errorf("%s is bound to %s (slot F%d) but the shift bar shows no label",
					name, k, pos+1)
			}
		}
	}
}

func TestShiftBarLabelsServersOnF2(t *testing.T) {
	items := ShiftItems(config.Default())

	if got := items[1].Label; got == "" {
		t.Fatal("want a label on Shift+F2 for the SSH server list")
	}
	if items[1].RawKey != "f14" {
		t.Errorf("want Shift+F2 to map to f14, got %q", items[1].RawKey)
	}
}

func TestShiftBarKeepsUnboundSlotsBlank(t *testing.T) {
	items := ShiftItems(config.Default())

	if len(items) != 10 {
		t.Fatalf("want 10 slots, got %d", len(items))
	}
	for i, itm := range items {
		if itm.Key != "F"+itoa(i+1) {
			t.Errorf("slot %d: want key F%d, got %q", i, i+1, itm.Key)
		}
	}
}

func TestDefaultBarLabelsEverySlot(t *testing.T) {
	for _, itm := range DefaultItems(config.Default()) {
		if strings.TrimSpace(itm.Label) == "" {
			t.Errorf("slot %q has no label", itm.Key)
		}
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
