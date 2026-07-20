package download

import (
	"fmt"
	"log"
	"time"

	"github.com/lmangani/llmirror/internal/auth"
	"github.com/lmangani/llmirror/internal/cache"
	"github.com/lmangani/llmirror/internal/hf"
	"github.com/lmangani/llmirror/internal/peer"
)

// Options controls the download resolution order.
type Options struct {
	HubDir      string
	RepoID      string
	RepoType    cache.RepoType
	Revision    string
	PeersFile   string
	PeerTimeout time.Duration
	SkipPeers   bool
	SkipHF      bool
	Token       string
	Group       string
	HFExtraArgs []string
}

// Resolve fetches a model: local cache → fleet peer → Hugging Face.
func Resolve(opts Options) error {
	if opts.Revision == "" {
		opts.Revision = "main"
	}
	if opts.RepoType == "" {
		opts.RepoType = cache.RepoModel
	}
	if opts.PeerTimeout == 0 {
		opts.PeerTimeout = 3 * time.Second
	}
	if opts.Token == "" {
		opts.Token, _ = auth.LoadToken("", "")
	}
	if opts.Group == "" {
		opts.Group, _ = auth.LoadGroup("", "")
	}

	ok, err := cache.HasRevision(opts.HubDir, opts.RepoID, opts.Revision, opts.RepoType)
	if err != nil {
		return err
	}
	if ok {
		log.Printf("llmirror: %s@%s already in local cache", opts.RepoID, opts.Revision)
		return nil
	}

	if !opts.SkipPeers {
		peers, err := peer.DiscoverPeers(opts.PeersFile, opts.PeerTimeout, opts.Group)
		if err != nil {
			log.Printf("llmirror: peer discovery warning: %v", err)
		}
		if len(peers) > 0 {
			peerURL, err := peer.FindPeerWithModel(peers, opts.RepoID, opts.Revision, opts.RepoType, opts.Token, opts.Group)
			if err == nil {
				log.Printf("llmirror: copying %s@%s from peer %s", opts.RepoID, opts.Revision, peerURL)
				if err := peer.CopyFromPeer(opts.HubDir, peerURL, opts.RepoID, opts.Revision, opts.RepoType, opts.Token, opts.Group); err != nil {
					log.Printf("llmirror: peer copy failed: %v", err)
				} else {
					return nil
				}
			} else {
				log.Printf("llmirror: no peer match: %v", err)
			}
		}
	}

	if opts.SkipHF {
		return fmt.Errorf("model %s@%s not found locally or on peers", opts.RepoID, opts.Revision)
	}

	log.Printf("llmirror: falling back to Hugging Face for %s@%s", opts.RepoID, opts.Revision)
	return hf.Download(opts.RepoID, opts.Revision, opts.HFExtraArgs)
}
