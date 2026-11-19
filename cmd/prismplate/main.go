package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"prismplate/internal/handover"
	"prismplate/internal/repository"
	"prismplate/internal/service"
	"prismplate/internal/transport"
	"prismplate/internal/validate"
	"syscall"
	"time"
)

// verifyHandover checks a delivered archive without touching any data root.
func verifyHandover(name string) error {
	raw, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	m, err := handover.Verify(raw)
	if err != nil {
		return err
	}
	out, _ := json.Marshal(map[string]any{
		"book": m.Book.ID, "revision": m.Book.Revision, "pages": len(m.Pages),
		"sources": len(m.Sources), "digest": m.Book.Digest,
	})
	fmt.Println(string(out))
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	addr := flag.String("listen", "127.0.0.1:7846", "HTTP address")
	root := flag.String("data", "var/prism-plate-v1", "persistent snapshot root")
	verify := flag.String("verify-handover", "", "verify a handover ZIP offline, then exit")
	flag.Parse()
	if *verify != "" {
		return verifyHandover(*verify)
	}
	repo, err := repository.Open(*root)
	if err != nil {
		return err
	}
	defer repo.Close()
	// A checksum proves identity, not semantic validity. Recompile recovered scenes.
	for _, p := range repo.View().Plates {
		for _, r := range p.Revisions {
			if _, err = validate.Compile(r.Document); err != nil {
				return err
			}
		}
	}
	svc := service.New(repo)
	defer svc.Close()
	server := &http.Server{Addr: *addr, Handler: transport.API{Service: svc}.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("prism-plate ready at %s", *addr)
	select {
	case err = <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
		<-done
	}
	return nil
}
