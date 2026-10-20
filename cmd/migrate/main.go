// Command migrate applies (up) or rolls back one (down) database migration.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sinhasandeep2006/kubesentinel/internal/storage"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		fmt.Fprintln(os.Stderr, "usage: migrate up|down   (database from KS_DATABASE_URL)")
		os.Exit(2)
	}
	url := os.Getenv("KS_DATABASE_URL")
	if url == "" {
		url = "postgres://kubesentinel:kubesentinel@127.0.0.1:5432/kubesentinel?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	s, err := storage.Open(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer s.Close()

	if os.Args[1] == "up" {
		n, err := s.MigrateUp(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("applied %d migration(s)\n", n)
		return
	}
	v, err := s.MigrateDown(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("rolled back migration %d\n", v)
}
