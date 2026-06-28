package render

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jalet/whyx/internal/layers"
	"github.com/jalet/whyx/internal/merge"
)

func sampleSteps() []merge.Step {
	return []merge.Step{
		{
			Layer:  layers.Layer{Kind: layers.KindChartDefaults},
			Values: map[string]any{"replicas": float64(1), "image": map[string]any{"tag": "dev"}},
		},
		{
			Layer:  layers.Layer{Kind: layers.KindCluster},
			Values: map[string]any{"replicas": float64(2), "image": map[string]any{"tag": "dev"}},
		},
		{
			Layer:  layers.Layer{Kind: layers.KindVersions},
			Values: map[string]any{"replicas": float64(2), "image": map[string]any{"tag": "prod"}},
		},
	}
}

func TestParseFormat(t *testing.T) {
	for _, s := range []string{"", "diff", "table", "json"} {
		if _, err := ParseFormat(s); err != nil {
			t.Errorf("ParseFormat(%q): unexpected error %v", s, err)
		}
	}
	if _, err := ParseFormat("xml"); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("ParseFormat(xml): want ErrUnknownFormat, got %v", err)
	}
}

func TestCascadeDiffFull(t *testing.T) {
	var out bytes.Buffer
	if err := Cascade(&out, sampleSteps(), Options{Format: FormatDiff}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	// Chart defaults (layer 1) are hidden by default, but overrides in later
	// layers still render against the default base ("~ replicas: 1 -> 2").
	if strings.Contains(got, "layer 1") || strings.Contains(got, "chart defaults") {
		t.Errorf("chart defaults should be hidden by default:\n%s", got)
	}
	for _, want := range []string{
		"@@ layer 5 · cluster · platform team @@", "  ~ replicas: 1 -> 2",
		"@@ layer 7 · promoted versions · Kargo (machine) @@", "  ~ image.tag: dev -> prod",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("color disabled but ANSI codes present:\n%s", got)
	}
}

func TestCascadeDiffChartDefaults(t *testing.T) {
	var out bytes.Buffer
	opts := Options{Format: FormatDiff, ShowChartDefaults: true}
	if err := Cascade(&out, sampleSteps(), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"@@ layer 1 · chart defaults · chart author @@",
		"  + image.tag: dev", "  + replicas: 1",
		"@@ layer 5 · cluster · platform team @@", "  ~ replicas: 1 -> 2",
		"@@ layer 7 · promoted versions · Kargo (machine) @@", "  ~ image.tag: dev -> prod",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCascadeDiffFocused(t *testing.T) {
	var out bytes.Buffer
	if err := Cascade(&out, sampleSteps(), Options{Format: FormatDiff, Key: "image.tag"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "replicas") {
		t.Errorf("focused image.tag should not mention replicas:\n%s", got)
	}
	if strings.Contains(got, "layer 5") {
		t.Errorf("focused mode should skip layer 5:\n%s", got)
	}
	for _, want := range []string{
		"@@ layer 1 · chart defaults · chart author @@", // origin kept in focused mode
		"  + image.tag: dev",
		"  ~ image.tag: dev -> prod",
		"= image.tag: prod",
		"set by layer 7 · promoted versions · Kargo (machine)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCascadeDiffColor(t *testing.T) {
	var out bytes.Buffer
	if err := Cascade(&out, sampleSteps(), Options{Format: FormatDiff, Color: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "\x1b[") {
		t.Error("color enabled but no ANSI codes emitted")
	}
}

func TestCascadeTable(t *testing.T) {
	var out bytes.Buffer
	if err := Cascade(&out, sampleSteps(), Options{Format: FormatTable}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"LAYER", "OWNER", "PATH", "image.tag", "dev -> prod"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in table:\n%s", want, got)
		}
	}
}

func TestCascadeJSON(t *testing.T) {
	var out bytes.Buffer
	if err := Cascade(&out, sampleSteps(), Options{Format: FormatJSON}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []struct {
		Index   int    `json:"index"`
		Owner   string `json:"owner"`
		Changes []struct {
			Op     string   `json:"op"`
			Path   []string `json:"path"`
			Dotted string   `json:"dotted"`
			New    any      `json:"new"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	// Chart defaults are hidden by default, leaving cluster + versions.
	if len(got) != 2 {
		t.Fatalf("want 2 layers, got %d", len(got))
	}
	for _, l := range got {
		if l.Index == 1 {
			t.Errorf("chart defaults (layer 1) should be hidden by default: %+v", got)
		}
	}
	last := got[len(got)-1]
	if last.Index != 7 {
		t.Errorf("last layer index: want 7, got %d", last.Index)
	}
	var found bool
	for _, ch := range last.Changes {
		if ch.Dotted == "image.tag" && ch.Op == "replace" && ch.New == "prod" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected image.tag replace->prod in last layer: %+v", last.Changes)
	}
}

func FuzzCascade(f *testing.F) {
	f.Add("a: 1\n", "a: 2\nb:\n  c: x\n")
	f.Add("", "k: [1, 2]\n")
	f.Add("image:\n  tag: dev\n", "image:\n  tag: prod\n")
	f.Fuzz(func(t *testing.T, y1, y2 string) {
		v1, err := merge.ParseValues([]byte(y1))
		if err != nil {
			return
		}
		v2, err := merge.ParseValues([]byte(y2))
		if err != nil {
			return
		}
		steps := []merge.Step{
			{Layer: layers.Layer{Kind: layers.KindChartDefaults}, Values: v1},
			{Layer: layers.Layer{Kind: layers.KindCluster}, Values: v2},
		}
		// Every format/color/key combination must render without panicking.
		for _, format := range []Format{FormatDiff, FormatTable, FormatJSON} {
			for _, color := range []bool{false, true} {
				for _, key := range []string{"", "a", "image.tag"} {
					opts := Options{Format: format, Color: color, Key: key}
					if err := Cascade(io.Discard, steps, opts); err != nil {
						t.Fatalf("Cascade(%v): %v", opts, err)
					}
				}
			}
		}
	})
}

func TestDisplayPath(t *testing.T) {
	tests := []struct {
		give []string
		want string
	}{
		{[]string{"image", "tag"}, "image.tag"},
		{[]string{"datasources", "datasources.yaml", "apiVersion"}, `datasources["datasources.yaml"].apiVersion`},
		{[]string{"a.b"}, `["a.b"]`},
		{[]string{"plain"}, "plain"},
	}
	for _, tt := range tests {
		if got := displayPath(tt.give); got != tt.want {
			t.Errorf("displayPath(%v): want %q, got %q", tt.give, tt.want, got)
		}
	}
}
