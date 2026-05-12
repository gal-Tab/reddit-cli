package cli

import (
	"encoding/json"
	"testing"
)

func TestParseListing_TopLevelArray(t *testing.T) {
	// Mimics /comments/<id>.json: two-element array of two Listings
	// (the first holds the post; the second holds the comment forest).
	raw := json.RawMessage(`[
		{"kind":"Listing","data":{"after":null,"children":[
			{"kind":"t3","data":{"id":"abc","name":"t3_abc","subreddit":"golang","score":42,"num_comments":7,"title":"hello"}}
		]}},
		{"kind":"Listing","data":{"after":"t1_xyz","children":[
			{"kind":"t1","data":{"id":"def","body":"first comment","score":10}}
		]}}
	]`)
	children, after, err := parseListing(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 children across both listings, got %d", len(children))
	}
	if after != "t1_xyz" {
		t.Fatalf("expected after=t1_xyz from second listing, got %q", after)
	}
}

func TestParseListing_NullAfter(t *testing.T) {
	raw := json.RawMessage(`{"kind":"Listing","data":{"after":null,"children":[]}}`)
	_, after, err := parseListing(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if after != "" {
		t.Fatalf("expected empty after on null, got %q", after)
	}
}

func TestParseListing_EmptyInput(t *testing.T) {
	children, after, err := parseListing(nil)
	if err != nil || len(children) != 0 || after != "" {
		t.Fatalf("expected empty results on nil input: children=%v after=%q err=%v", children, after, err)
	}
}

func TestExtractPosts_SkipsCommentsAndDeleted(t *testing.T) {
	children := []json.RawMessage{
		json.RawMessage(`{"id":"abc","subreddit":"golang","score":42,"num_comments":7,"title":"hello"}`),
		json.RawMessage(`{"body":"a comment with no id"}`), // skipped: no ID
		json.RawMessage(`not valid json`),                   // skipped: parse fail
		json.RawMessage(`{"id":"def","subreddit":"rust","score":10,"name":"t3_def"}`),
	}
	posts := extractPosts(children)
	if len(posts) != 2 {
		t.Fatalf("expected 2 valid posts, got %d", len(posts))
	}
	if posts[0].Name != "t3_abc" {
		t.Fatalf("expected fullname back-filled: got %q", posts[0].Name)
	}
	if posts[1].Name != "t3_def" {
		t.Fatalf("expected explicit name preserved: got %q", posts[1].Name)
	}
}

func TestJSONNum_HandlesShapes(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want float64
	}{
		{"float", 3.14, 3.14},
		{"int-as-float", float64(42), 42},
		{"string number", "100", 100},
		{"string non-number", "nope", 0},
		{"nil", nil, 0},
		{"json.Number", json.Number("99"), 99},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := jsonNum(c.in)
			if got != c.want {
				t.Fatalf("jsonNum(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
