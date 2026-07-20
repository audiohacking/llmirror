package cache

import "testing"

func TestRepoFolderRoundTrip(t *testing.T) {
	repo := "meta-llama/Llama-2-7b-hf"
	folder := RepoFolderName(repo)
	if folder != "models--meta-llama--Llama-2-7b-hf" {
		t.Fatalf("unexpected folder: %s", folder)
	}
	back, err := RepoIDFromFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if back != repo {
		t.Fatalf("got %q want %q", back, repo)
	}
}

func TestHubDirRespectsEnv(t *testing.T) {
	t.Setenv("HF_HUB_CACHE", "/tmp/custom-hub")
	dir, err := HubDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != "/tmp/custom-hub" {
		t.Fatalf("got %q", dir)
	}
}
