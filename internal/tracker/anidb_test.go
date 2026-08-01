package tracker

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestParseAniDBTitlesUsesMainTitleAndAliases(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(`<?xml version="1.0"?><animetitles>
<anime aid="10"><title type="official">Example English</title><title type="main">Example Main</title><title type="synonym">Sample</title></anime>
<anime aid="20"><title type="main">Other</title></anime>
</animetitles>`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	candidates, err := parseAniDBTitles(&compressed, "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != "10" || candidates[0].Title != "Example Main" || len(candidates[0].Aliases) != 3 {
		t.Fatalf("candidates = %#v", candidates)
	}
}
