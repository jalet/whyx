package diff

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBetween(t *testing.T) {
	tests := []struct {
		name       string
		give, want map[string]any
		wantChange []Change
	}{
		{
			name:       "add scalar",
			give:       map[string]any{},
			want:       map[string]any{"a": float64(1)},
			wantChange: []Change{{Path: []string{"a"}, Op: OpAdd, New: float64(1)}},
		},
		{
			name:       "replace scalar",
			give:       map[string]any{"a": float64(1)},
			want:       map[string]any{"a": float64(2)},
			wantChange: []Change{{Path: []string{"a"}, Op: OpReplace, Old: float64(1), New: float64(2)}},
		},
		{
			name:       "remove scalar",
			give:       map[string]any{"a": float64(1)},
			want:       map[string]any{},
			wantChange: []Change{{Path: []string{"a"}, Op: OpRemove, Old: float64(1)}},
		},
		{
			name: "deep map changes only the differing leaf",
			give: map[string]any{"image": map[string]any{"repo": "app", "tag": "dev"}},
			want: map[string]any{"image": map[string]any{"repo": "app", "tag": "prod"}},
			wantChange: []Change{
				{Path: []string{"image", "tag"}, Op: OpReplace, Old: "dev", New: "prod"},
			},
		},
		{
			name: "list replaced wholesale",
			give: map[string]any{"ports": []any{float64(80)}},
			want: map[string]any{"ports": []any{float64(8080), float64(8443)}},
			wantChange: []Change{{
				Path: []string{"ports"}, Op: OpReplace,
				Old: []any{float64(80)}, New: []any{float64(8080), float64(8443)},
			}},
		},
		{
			name: "key containing a dot keeps distinct segments",
			give: map[string]any{"ds": map[string]any{"a.yaml": float64(1)}},
			want: map[string]any{"ds": map[string]any{"a.yaml": float64(2)}},
			wantChange: []Change{{
				Path: []string{"ds", "a.yaml"}, Op: OpReplace, Old: float64(1), New: float64(2),
			}},
		},
		{
			name: "scalar becomes a subtree",
			give: map[string]any{"a": float64(1)},
			want: map[string]any{"a": map[string]any{"b": float64(2)}},
			wantChange: []Change{
				{Path: []string{"a"}, Op: OpRemove, Old: float64(1)},
				{Path: []string{"a", "b"}, Op: OpAdd, New: float64(2)},
			},
		},
		{
			name:       "no change",
			give:       map[string]any{"a": float64(1), "b": map[string]any{"c": "x"}},
			want:       map[string]any{"a": float64(1), "b": map[string]any{"c": "x"}},
			wantChange: []Change{},
		},
		{
			name:       "empty map is a leaf",
			give:       map[string]any{},
			want:       map[string]any{"cfg": map[string]any{}},
			wantChange: []Change{{Path: []string{"cfg"}, Op: OpAdd, New: map[string]any{}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Between(tt.give, tt.want)
			if diff := cmp.Diff(tt.wantChange, got); diff != "" {
				t.Errorf("changes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDottedAndSymbol(t *testing.T) {
	c := Change{Path: []string{"image", "tag"}, Op: OpReplace}
	if c.Dotted() != "image.tag" {
		t.Errorf("Dotted: got %q", c.Dotted())
	}
	tests := map[Op]struct{ symbol, name string }{
		OpAdd:     {"+", "add"},
		OpReplace: {"~", "replace"},
		OpRemove:  {"-", "remove"},
	}
	for op, want := range tests {
		if op.Symbol() != want.symbol {
			t.Errorf("Symbol(%d): want %q, got %q", op, want.symbol, op.Symbol())
		}
		if op.String() != want.name {
			t.Errorf("String(%d): want %q, got %q", op, want.name, op.String())
		}
	}
}
