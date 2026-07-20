package peer

import "testing"

func TestGroupMatch(t *testing.T) {
	cases := []struct {
		local string
		txt   []string
		want  bool
	}{
		{"", nil, true},
		{"", []string{"group=lab"}, true},
		{"lab", []string{"group=lab", "v=1"}, true},
		{"lab", []string{"group=other"}, false},
		{"lab", []string{"v=1"}, false},
		{"lab", nil, false},
	}
	for _, tc := range cases {
		if got := groupMatch(tc.local, tc.txt); got != tc.want {
			t.Errorf("local=%q txt=%v: got %v want %v", tc.local, tc.txt, got, tc.want)
		}
	}
}

func TestTxtValue(t *testing.T) {
	if got := txtValue([]string{"v=1", "group=acme"}, "group"); got != "acme" {
		t.Fatalf("got %q", got)
	}
	if got := txtValue([]string{"v=1"}, "group"); got != "" {
		t.Fatalf("got %q", got)
	}
}
