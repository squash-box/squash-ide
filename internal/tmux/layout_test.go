package tmux

import (
	"reflect"
	"strings"
	"testing"
)

func TestTile_HappyPath(t *testing.T) {
	tests := []struct {
		name              string
		totalCols, tuiW   int
		n, minW           int
		want              []int
	}{
		{
			// Ultrawide example from the task spec: 240 cols, 60 col TUI,
			// 3 right panes, min 80 → avail = 240-60-3 = 177; per = 59 → reject
			// would happen below; this case picks numbers that pass cleanly.
			name:      "ultrawide-3panes-clean",
			totalCols: 360, tuiW: 60,
			n: 3, minW: 80,
			// avail = 360 - 60 - 3 = 297; 297/3 = 99
			want: []int{99, 99, 99},
		},
		{
			name:      "single-pane-fills-rest",
			totalCols: 200, tuiW: 60,
			n: 1, minW: 80,
			// avail = 200 - 60 - 1 = 139
			want: []int{139},
		},
		{
			name:      "two-panes-even",
			totalCols: 262, tuiW: 60,
			n: 2, minW: 80,
			// avail = 262 - 60 - 2 = 200; 200/2 = 100
			want: []int{100, 100},
		},
		{
			name:      "two-panes-with-remainder-goes-to-leftmost",
			totalCols: 263, tuiW: 60,
			n: 2, minW: 80,
			// avail = 263 - 60 - 2 = 201; 201/2 = 100 r 1 → first gets +1
			want: []int{101, 100},
		},
		{
			name:      "four-panes-with-remainder-distributed",
			totalCols: 425, tuiW: 60,
			n: 4, minW: 80,
			// avail = 425 - 60 - 4 = 361; 361/4 = 90 r 1 → first pane +1
			want: []int{91, 90, 90, 90},
		},
		{
			name:      "exact-fit-at-minimum-allowed",
			totalCols: 60 + 4 + 4*80, // tuiW + borders + n*minW
			tuiW:      60,
			n:         4, minW: 80,
			// avail = 60+4+320 - 60 - 4 = 320; 320/4 = 80 (exactly minimum)
			want: []int{80, 80, 80, 80},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Tile(tt.totalCols, tt.tuiW, tt.n, tt.minW)
			if err != nil {
				t.Fatalf("Tile: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Tile = %v, want %v", got, tt.want)
			}
			// Sanity: widths sum to (total - tui - n), and every pane >= minW.
			sum := 0
			for _, w := range got {
				sum += w
				if w < tt.minW {
					t.Errorf("pane width %d below minimum %d", w, tt.minW)
				}
			}
			if want := tt.totalCols - tt.tuiW - tt.n; sum != want {
				t.Errorf("sum of widths = %d, want %d", sum, want)
			}
			// Widths within 1 of each other.
			for i := 1; i < len(got); i++ {
				diff := got[i-1] - got[i]
				if diff < 0 || diff > 1 {
					t.Errorf("widths not balanced: got[%d]=%d got[%d]=%d (diff %d)",
						i-1, got[i-1], i, got[i], diff)
				}
			}
		})
	}
}

func TestTile_RejectsWhenBelowMin(t *testing.T) {
	tests := []struct {
		name              string
		totalCols, tuiW   int
		n, minW           int
	}{
		{
			// Adding a 4th pane to a barely-fits-3 layout should reject.
			name:      "four-panes-too-narrow",
			totalCols: 60 + 4 + 3*80, // fits 3 with min=80, not 4
			tuiW:      60, n: 4, minW: 80,
		},
		{
			// Two big panes won't fit on a tight screen.
			name:      "two-panes-tiny-window",
			totalCols: 200, tuiW: 60,
			n: 2, minW: 80,
			// avail = 200-60-2 = 138; 138/2 = 69 < 80
		},
		{
			// One pane below floor.
			name:      "one-pane-below-floor",
			totalCols: 100, tuiW: 60,
			n: 1, minW: 80,
			// avail = 100-60-1 = 39 < 80
		},
		{
			// TUI alone wider than total — degenerate.
			name:      "tui-wider-than-window",
			totalCols: 50, tuiW: 60,
			n: 1, minW: 80,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Tile(tt.totalCols, tt.tuiW, tt.n, tt.minW)
			if err == nil {
				t.Fatalf("Tile: expected rejection, got widths %v", got)
			}
			// Error message should mention the relevant numbers.
			msg := err.Error()
			for _, frag := range []string{"total=", "tui=", "panes=", "min="} {
				if !strings.Contains(msg, frag) {
					t.Errorf("error %q missing fragment %q", msg, frag)
				}
			}
		})
	}
}

func TestTile_BadInputs(t *testing.T) {
	tests := []struct {
		name             string
		total, tui, n, m int
		wantSubstr       string
	}{
		{"zero-panes", 200, 60, 0, 80, "n must be >= 1"},
		{"negative-panes", 200, 60, -1, 80, "n must be >= 1"},
		{"zero-tui", 200, 0, 1, 80, "tuiWidth must be >= 1"},
		{"zero-min", 200, 60, 1, 0, "minWidth must be >= 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Tile(tt.total, tt.tui, tt.n, tt.m)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Errorf("error %q missing substring %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}
