package engine

import (
	"reflect"
	"testing"
)

func TestLocaleValues(t *testing.T) {
	acceptLanguage, languages, err := localeValues("fr_FR")
	if err != nil {
		t.Fatalf("localeValues: %v", err)
	}
	if acceptLanguage != "fr-FR,fr;q=0.9,en-US;q=0.8,en;q=0.7" {
		t.Fatalf("acceptLanguage = %q", acceptLanguage)
	}
	wantLanguages := []string{"fr-FR", "fr", "en-US", "en"}
	if !reflect.DeepEqual(languages, wantLanguages) {
		t.Fatalf("languages = %#v, want %#v", languages, wantLanguages)
	}
}

func TestLocaleValuesRejectsEmptyLocale(t *testing.T) {
	if _, _, err := localeValues(" "); err == nil {
		t.Fatal("expected empty locale error")
	}
}
