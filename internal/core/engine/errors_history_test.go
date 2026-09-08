package engine

import "testing"

func TestErrorsFromEventsIncludesConsoleAndNetwork(t *testing.T) {
	events := []ObserverEvent{
		{Kind: KindConsole, Level: "info", Text: "ignore"},
		{Kind: KindConsole, Level: "error", Text: "console error"},
		{Kind: KindError, Level: "error", Text: "exception"},
		{Kind: KindNet, Status: 404, URL: "https://example.test/missing"},
		{Kind: KindNet, Failed: "net::ERR_CONNECTION_REFUSED", URL: "https://example.test/offline"},
		{Kind: KindNet, Status: 200},
	}
	errors := ErrorsFromEvents(events)
	if len(errors) != 4 || errors[0].Message != "console error" || errors[2].Status != 404 || errors[3].Type != "network" {
		t.Fatalf("unexpected errors: %+v", errors)
	}
	if got := ErrorsFromEvents(nil); got == nil {
		t.Fatal("empty errors must serialize as []")
	}
}
