// Package charset holds the character sets used to judge whether a custom
// font can stand in for a Chinese UI font.
package charset

import (
	_ "embed"
	"sort"
)

var (
	//go:embed data/gb2312-level1.txt
	gb2312L1 string
	//go:embed data/gb2312-level2.txt
	gb2312L2 string
	//go:embed data/big5-level1.txt
	big5L1 string
	//go:embed data/cjk-punctuation.txt
	cjkPunct string
)

// Set is a named list of code points.
type Set struct {
	ID    string
	Name  string
	Runes []rune
}

func runes(s string) []rune {
	r := []rune(s)
	sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
	return r
}

func span(lo, hi rune) []rune {
	out := make([]rune, 0, hi-lo+1)
	for c := lo; c <= hi; c++ {
		out = append(out, c)
	}
	return out
}

var (
	ASCII       = Set{"ascii", "ASCII 可打印字符", span(0x20, 0x7E)}
	Latin1      = Set{"latin1", "Latin-1 补充", span(0xA0, 0xFF)}
	GB2312L1    = Set{"gb2312-1", "GB2312 一级汉字", runes(gb2312L1)}
	GB2312L2    = Set{"gb2312-2", "GB2312 二级汉字", runes(gb2312L2)}
	Big5L1      = Set{"big5-1", "Big5 常用字（繁体）", runes(big5L1)}
	Punctuation = Set{"punct", "中文标点与全角符号", runes(cjkPunct)}
)

// All is the report order.
var All = []Set{ASCII, Latin1, Punctuation, GB2312L1, GB2312L2, Big5L1}

// Coverage counts how many of set's runes have a mapping.
type Coverage struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Have    int     `json:"have"`
	Total   int     `json:"total"`
	Ratio   float64 `json:"ratio"`
	Missing string  `json:"missing,omitempty"` // first few missing characters
}

func Measure(set Set, has func(rune) bool) Coverage {
	c := Coverage{ID: set.ID, Name: set.Name, Total: len(set.Runes)}
	var miss []rune
	for _, r := range set.Runes {
		if has(r) {
			c.Have++
		} else if len(miss) < 12 {
			miss = append(miss, r)
		}
	}
	if c.Total > 0 {
		c.Ratio = float64(c.Have) / float64(c.Total)
	}
	if set.ID != "ascii" && set.ID != "latin1" {
		c.Missing = string(miss)
	} else if len(miss) > 0 {
		for _, r := range miss {
			if r == 0x20 || r == 0xA0 || r == 0xAD {
				continue
			}
			c.Missing += string(r)
		}
	}
	return c
}
