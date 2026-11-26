package stories

import (
	"reflect"
	"testing"
)

func TestParsePinned(t *testing.T) {
	cases := []struct {
		in   string
		want []int64
	}{
		{"", nil},
		{" 1, 5 ,x,-2,0,5,7", []int64{1, 5, 7}},
		{"1,2,3,4,5,6,7,8,9,10,11,12", []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"99999999999999999999,3", []int64{3}},
	}
	for _, c := range cases {
		if got := parsePinned(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parsePinned(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestPinArg(t *testing.T) {
	if got := pinArg([]int64{1, 5, 7}); got != "1,5,7" {
		t.Errorf("pinArg = %q", got)
	}
	if got := pinArg(nil); got != "" {
		t.Errorf("pinArg(nil) = %q", got)
	}
}
