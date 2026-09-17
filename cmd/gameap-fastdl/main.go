package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	"github.com/gameap/gameap-fastdl/internal/app"
	"github.com/gameap/gameap-fastdl/internal/config"
	"github.com/gameap/gameap-fastdl/internal/gameconfig"
)

var version = "0.1.0"

var (
	errUsage               = errors.New("usage: gameap-fastdl serve|service|validate --config <file> | version")
	errUnexpectedArguments = errors.New("unexpected arguments")
	errUnknownCommand      = errors.New("unknown command")
	errConfigureFlags      = errors.New("configure requires --root and --engine")
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	if err := run(os.Args[1:]); err != nil {
		slog.Error("FastDL stopped", "error", err)

		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errUsage
	}

	if args[0] == "version" {
		_, _ = fmt.Fprintln(os.Stdout, "gameap-fastdl "+version)

		return nil
	}

	if args[0] == "configure" {
		return runConfigure(args[1:])
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	filename := flags.String("config", "config.json", "configuration file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errUnexpectedArguments
	}

	switch args[0] {
	case "validate":
		cfg, err := config.Load(*filename)
		if err != nil {
			return err
		}

		_, errs := config.Servers(cfg.ServersDir)
		if len(errs) > 0 {
			return errors.Join(errs...)
		}

		_, _ = fmt.Fprintln(os.Stdout, "Configuration is valid")

		return nil
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, terminationSignal())
		defer stop()

		return app.Run(ctx, *filename, nil)
	case "service":
		return service(*filename)
	default:
		return errUnknownCommand
	}
}

func runConfigure(args []string) error {
	flags := flag.NewFlagSet("configure", flag.ContinueOnError)
	root := flags.String("root", "", "absolute game server base directory")
	gameDir := flags.String("game-dir", "", "relative mod directory")
	engine := flags.String("engine", "", "source or goldsource")
	publicURL := flags.String("url", "", "public FastDL URL; omit to remove managed block")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *root == "" {
		return errConfigureFlags
	}

	if err := gameconfig.Configure(*root, *gameDir, *engine, *publicURL); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(os.Stdout, `{"configured":true}`)

	return nil
}
