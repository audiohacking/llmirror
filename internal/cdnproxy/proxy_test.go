package cdnproxy

import "testing"

func TestParseResolvePath(t *testing.T) {
	cases := []struct {
		path    string
		ok      bool
		repo    string
		rev     string
		file    string
	}{
		{
			path: "/meta-llama/Llama-2-7b-hf/resolve/main/config.json",
			ok:   true, repo: "meta-llama/Llama-2-7b-hf", rev: "main", file: "config.json",
		},
		{
			path: "/gpt2/resolve/main/pytorch_model.bin",
			ok:   true, repo: "gpt2", rev: "main", file: "pytorch_model.bin",
		},
		{
			path: "/models/org/name/resolve/abc123/tokenizer.json",
			ok:   true, repo: "org/name", rev: "abc123", file: "tokenizer.json",
		},
		{
			path: "/org/name/raw/main/folder/file.txt",
			ok:   true, repo: "org/name", rev: "main", file: "folder/file.txt",
		},
		{
			path: "/datasets/glue/resolve/main/data.csv",
			ok:   false, // datasets not served from model cache
		},
		{
			path: "/api/models/gpt2",
			ok:   false,
		},
		{
			path: "/org/name/tree/main",
			ok:   false,
		},
	}

	for _, tc := range cases {
		got, ok := ParseResolvePath(tc.path)
		if ok != tc.ok {
			t.Errorf("%s: ok=%v want %v", tc.path, ok, tc.ok)
			continue
		}
		if !tc.ok {
			continue
		}
		if got.RepoID != tc.repo || got.Revision != tc.rev || got.Filename != tc.file {
			t.Errorf("%s: got %+v", tc.path, got)
		}
	}
}
