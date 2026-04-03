package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/jookos/obgo/internal/config"
	"github.com/jookos/obgo/internal/couchdb"
	"github.com/jookos/obgo/internal/crypto"
	"github.com/jookos/obgo/internal/sync"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var envFile string

	root := &cobra.Command{
		Use:   "obgo",
		Short: "Sync an Obsidian vault with a CouchDB instance using the Livesync protocol",
	}

	root.PersistentFlags().StringVar(&envFile, "env-file", ".env", "path to .env file")

	root.AddCommand(pullCmd(&envFile))
	root.AddCommand(pushCmd(&envFile))

	return root
}

func pullCmd(envFile *string) *cobra.Command {
	var watch bool

	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull documents from CouchDB to local vault",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), *envFile, watch, true)
		},
	}
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "keep watching for remote changes after pull")
	return cmd
}

func pushCmd(envFile *string) *cobra.Command {
	var watch bool

	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local vault files to CouchDB",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), *envFile, watch, false)
		},
	}
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "keep watching for local changes after push")
	return cmd
}

func run(parentCtx context.Context, envFile string, watch bool, isPull bool) error {
	// Load .env file if present; ignore error if file is missing.
	_ = godotenv.Load(envFile)

	// Load configuration from environment.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	// Create CouchDB client.
	db, err := couchdb.New(cfg.CouchDBURL)
	if err != nil {
		return fmt.Errorf("CouchDB client error: %w", err)
	}

	// Create crypto service.
	cr := crypto.New(cfg.E2EEPassword)

	// Create sync service.
	svc := sync.New(db, cr, cfg.DataPath)

	// Set up context with cancellation for graceful shutdown.
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// Handle OS signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Run the primary operation.
	if isPull {
		if err := svc.Pull(ctx); err != nil {
			return fmt.Errorf("pull failed: %w", err)
		}
	} else {
		if err := svc.Push(ctx); err != nil {
			return fmt.Errorf("push failed: %w", err)
		}
	}

	// Start watch mode if requested.
	if watch {
		if err := svc.Watch(ctx); err != nil {
			return fmt.Errorf("watch failed: %w", err)
		}
	}

	return nil
}
