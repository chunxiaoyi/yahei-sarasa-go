package display

import (
	"strings"
	"testing"
)

func mon(scale int, primary bool) Monitor {
	return Monitor{Width: 2560, Height: 1440, DPI: scale * 96 / 100, Scale: scale, Primary: primary}
}

func TestRecommend(t *testing.T) {
	cases := []struct {
		name   string
		info   Info
		hinted bool
		want   string
	}{
		{"unsupported", Info{}, true, "无法检测"},
		{"no monitors", Info{Supported: true}, true, "无法检测"},
		{"100%", Info{Supported: true, Monitors: []Monitor{mon(100, true)}, Smoothing: SmoothingClearType}, true, "缩放 100%"},
		{"125%", Info{Supported: true, Monitors: []Monitor{mon(125, true)}}, true, "缩放 125%"},
		{"150%", Info{Supported: true, Monitors: []Monitor{mon(150, true)}}, false, "150%"},
		{"200%", Info{Supported: true, Monitors: []Monitor{mon(200, true)}}, false, "约 24 像素"},
		{"laptop 200% + external 100%", Info{Supported: true, Monitors: []Monitor{mon(200, true), mon(100, false)}}, true, "屏幕 2"},
		{"two 4K at 150%", Info{Supported: true, Monitors: []Monitor{mon(150, true), mon(175, false)}}, false, "所有屏幕"},
		{"smoothing off at 200%", Info{Supported: true, Monitors: []Monitor{mon(200, true)}, Smoothing: SmoothingOff}, true, "字体平滑"},
	}
	for _, c := range cases {
		r := Recommend(c.info)
		if r.Hinted != c.hinted || !strings.Contains(r.Reason, c.want) {
			t.Errorf("%s: hinted=%v reason=%q", c.name, r.Hinted, r.Reason)
		}
	}
}

func TestScaleFromDPI(t *testing.T) {
	for dpi, want := range map[int]int{96: 100, 120: 125, 144: 150, 168: 175, 192: 200, 0: 0} {
		if got := ScaleFromDPI(dpi); got != want {
			t.Fatalf("%d -> %d want %d", dpi, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	low := func() Info { return Info{Supported: true, Monitors: []Monitor{mon(100, true)}} }
	high := func() Info { return Info{Supported: true, Monitors: []Monitor{mon(200, true)}} }
	if h, _ := Resolve(ModeAuto, low); !h {
		t.Fatal("auto at 100% should be hinted")
	}
	if h, n := Resolve(ModeAuto, high); h || !strings.HasPrefix(n, "unhinted（自动判定") {
		t.Fatalf("auto at 200%%: %v %q", h, n)
	}
	called := false
	if h, _ := Resolve(ModeUnhinted, func() Info { called = true; return low() }); h || called {
		t.Fatal("manual mode must not detect or override")
	}
	if _, err := ParseMode("maybe"); err == nil {
		t.Fatal("bad mode accepted")
	}
}
