package content

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteJSONIsValid(t *testing.T) {
	if _, err := LoadFile(filepath.Join("..", "..", "content", "site.json")); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsDuplicates(t *testing.T) {
	site := `{
		"conference": {"acronym": "ICAS", "name": "Test"},
		"tracks": [{"key": "with-workshop"}, {"key": "without-workshop"}],
		"calls": [{"key": "with-workshop", "status": "open"}, {"key": "with-workshop", "status": "open"}],
		"topics": [{"title": "Drones"}, {"title": "Drones"}],
		"downloads": [{"key": "speaker-kit"}, {"key": "speaker-kit"}, {"key": "Not A Slug"}]
	}`
	_, err := Load(strings.NewReader(site))
	if err == nil {
		t.Fatal("duplicate calls, topics and download groups were accepted")
	}
	for _, want := range []string{
		`calls[1].key "with-workshop" is used twice`,
		`topics[1].title "Drones" is used twice`,
		`downloads[1].key "speaker-kit" is used twice`,
		`downloads[2].key "Not A Slug" must be lowercase words joined by hyphens`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}
