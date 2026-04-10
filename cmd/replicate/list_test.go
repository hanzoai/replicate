package main_test

import (
	"context"
	"testing"

	"github.com/hanzoai/replicate"
	main "github.com/hanzoai/replicate/cmd/replicate"
	"github.com/hanzoai/replicate/internal/testingutil"
)

func TestListCommand_Run(t *testing.T) {
	t.Run("TooManyArguments", func(t *testing.T) {
		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"extra-arg"})
		if err == nil {
			t.Error("expected error for too many arguments")
		}
		if err.Error() != "too many arguments" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ConnectionError", func(t *testing.T) {
		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"-socket", "/nonexistent/socket.sock"})
		if err == nil {
			t.Error("expected error for socket connection failure")
		}
	})

	t.Run("CustomTimeout", func(t *testing.T) {
		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"-socket", "/nonexistent/socket.sock", "-timeout", "1"})
		if err == nil {
			t.Error("expected error for socket connection failure")
		}
	})

	t.Run("InvalidTimeoutZero", func(t *testing.T) {
		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"-timeout", "0"})
		if err == nil {
			t.Error("expected error for zero timeout")
		}
		if err.Error() != "timeout must be greater than 0" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("InvalidTimeoutNegative", func(t *testing.T) {
		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"-timeout", "-1"})
		if err == nil {
			t.Error("expected error for negative timeout")
		}
		if err.Error() != "timeout must be greater than 0" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Success", func(t *testing.T) {
		db, sqldb := testingutil.MustOpenDBs(t)
		defer testingutil.MustCloseDBs(t, db, sqldb)

		store := replicate.NewStore([]*replicate.DB{db}, replicate.CompactionLevels{{Level: 0}})
		store.CompactionMonitorEnabled = false
		if err := store.Open(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer store.Close(context.Background())

		server := replicate.NewServer(store)
		server.SocketPath = testSocketPath(t)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer server.Close()

		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"-socket", server.SocketPath})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("SuccessEmpty", func(t *testing.T) {
		store := replicate.NewStore(nil, replicate.CompactionLevels{{Level: 0}})
		store.CompactionMonitorEnabled = false
		if err := store.Open(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer store.Close(context.Background())

		server := replicate.NewServer(store)
		server.SocketPath = testSocketPath(t)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer server.Close()

		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"-socket", server.SocketPath})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("JSONOutput", func(t *testing.T) {
		db, sqldb := testingutil.MustOpenDBs(t)
		defer testingutil.MustCloseDBs(t, db, sqldb)

		store := replicate.NewStore([]*replicate.DB{db}, replicate.CompactionLevels{{Level: 0}})
		store.CompactionMonitorEnabled = false
		if err := store.Open(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer store.Close(context.Background())

		server := replicate.NewServer(store)
		server.SocketPath = testSocketPath(t)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		defer server.Close()

		cmd := &main.ListCommand{}
		err := cmd.Run(context.Background(), []string{"-socket", server.SocketPath, "-json"})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
