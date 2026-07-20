package cache

import "testing"

func TestRepoFolderRoundTrip(t *testing.T) {
	repo := "meta-llama/Llama-2-7b-hf"
	folder := RepoFolderName(repo)
	if folder != "models--meta-llama--Llama-2-7b-hf" {
		t.Fatalf("unexpected folder: %s", folder)
	}
	back, typ, err := RepoIDFromFolder(folder)
	if err != nil {
		t.Fatal(err)
	}
	if back != repo || typ != RepoModel {
		t.Fatalf("got %q %q", back, typ)
	}

	ds := RepoFolderName("glue", RepoDataset)
	if ds != "datasets--glue" {
		t.Fatalf("dataset folder: %s", ds)
	}
	id, typ, err := RepoIDFromFolder(ds)
	if err != nil || id != "glue" || typ != RepoDataset {
		t.Fatalf("got %q %q %v", id, typ, err)
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
