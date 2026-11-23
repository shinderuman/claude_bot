package facttool

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeReport(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want []string
	}{
		{name: "empty", data: `[]`, want: []string{"Total Facts: 0", "Duplication Rate: 0.00%"}},
		{
			name: "duplicate",
			data: `[{"target":"user","key":"a","value":"x"},{"target":"user","key":"a","value":"x "},{"target":"user","key":"b","value":"y"}]`,
			want: []string{"Total Facts: 3", "Unique Facts (Target+Key+Value): 2", "Duplication Rate: 33.33%", "user (Key: a) - 2 entries"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "facts.json")
			if err := os.WriteFile(file, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := Analyze(file, &out); err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("report lacks %q:\n%s", want, &out)
				}
			}
		})
	}
}
