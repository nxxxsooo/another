package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/nxxxsooo/another/internal/config"
	"github.com/nxxxsooo/another/internal/doubaoevents"
	"github.com/nxxxsooo/another/internal/doubaoservice"
	"github.com/nxxxsooo/another/internal/registry"
	"github.com/nxxxsooo/another/internal/titlemark"
	"github.com/nxxxsooo/another/internal/titlepolicy"
	"github.com/spf13/cobra"
)

var installDoubaoService = doubaoservice.Install
var removeDoubaoService = doubaoservice.Remove
var validateDoubaoPolicy = func(settings config.Settings) error {
	if err := titlepolicy.Validate(settings, registry.NewEnabled([]string{"doubao"})); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return doubaoevents.ErrUnsupported
	}
	options, err := doubaoevents.DefaultOptions()
	if err != nil {
		return err
	}
	for _, dir := range []string{options.DesktopDir, options.CLIDir} {
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("doubao event source: %w", err)
		}
	}
	return fmt.Errorf("doubao native completion log source unavailable; start Doubao desktop or CLI first")
}

func (a *App) titlePolicyCmd() *cobra.Command {
	var enable, disable, background, watch bool
	cmd := &cobra.Command{Use: "title-policy", Short: "Manage opt-in automatic title policies"}
	doubao := &cobra.Command{Use: "doubao", Short: "Name Doubao sessions on completed-turn events", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if watch {
				if enable || disable || background {
					return fmt.Errorf("--watch cannot be combined with enable, disable or background")
				}
				settings, err := config.LoadSettings()
				if err != nil {
					return err
				}
				if !settings.TitlePolicy.DoubaoEvents {
					return fmt.Errorf("doubao event title policy is disabled")
				}
				providerEnabled := false
				for _, id := range settings.EnabledProviders {
					if id == "doubao" {
						providerEnabled = true
					}
				}
				if !providerEnabled {
					return fmt.Errorf("doubao provider is disabled")
				}
				if err := validateDoubaoPolicy(settings); err != nil {
					return err
				}
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				options, err := doubaoevents.DefaultOptions()
				if err != nil {
					return err
				}
				options.StatePath = filepath.Join(config.CacheDir(), "doubao-events.json")
				options.OnError = func(_ doubaoevents.Event, _ error) {
					fmt.Fprintln(cmd.ErrOrStderr(), "Doubao event naming failed; run 'title-policy doubao' for status")
				}
				readyErrors := make(chan error, 1)
				watchCtx, cancelWatch := context.WithCancel(ctx)
				defer cancelWatch()
				options.Ready = func() {
					if err := titlepolicy.RecordListenerReady(); err != nil {
						readyErrors <- err
						cancelWatch()
					}
				}
				watcher, err := doubaoevents.New(options)
				if err != nil {
					return err
				}
				err = watcher.Watch(watchCtx, func(ctx context.Context, event doubaoevents.Event) error {
					settings, err := config.LoadSettings()
					if err != nil {
						return err
					}
					if err := titlepolicy.OnEvent(ctx, registry.NewEnabled(settings.EnabledProviders), event.ConversationID); err != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						return err
					}
					return nil
				})
				select {
				case readyErr := <-readyErrors:
					return fmt.Errorf("record listener readiness: %w", readyErr)
				default:
				}
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return nil
				}
				return err
			}
			if !enable && !disable && !background {
				settings, err := config.LoadSettings()
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Doubao event title policy: %t\nEnabled since (Unix seconds): %d\n", settings.TitlePolicy.DoubaoEvents, settings.TitlePolicy.DoubaoEnabledAt)
				listener, readyErr := titlepolicy.LoadListenerStatus()
				if readyErr == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Listener last ready: %s; pid=%d\n", listener.ReadyAt.Format(time.RFC3339Nano), listener.PID)
				} else if os.IsNotExist(readyErr) {
					fmt.Fprintln(cmd.OutOrStdout(), "Listener last ready: never")
				} else {
					return readyErr
				}
				service, serviceErr := doubaoservice.Status()
				if serviceErr != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Background service unavailable: %s\n", serviceErr)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Background service: %s\n", service)
				}
				status, err := titlepolicy.LoadStatus()
				if os.IsNotExist(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "Last event naming: none")
					return nil
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Last event naming: %s; checked=%d renamed=%d failed=%d\n", status.At.Format(time.RFC3339), status.Checked, status.Renamed, status.Failed)
				if !status.LastRenamedAt.IsZero() {
					fmt.Fprintf(cmd.OutOrStdout(), "Last automatic rename: %s; %s\n", status.LastRenamedAt.Format(time.RFC3339), status.LastRenamedTitle)
				}
				if latest, ok := titlemark.Open().Latest("doubao"); ok {
					fmt.Fprintf(cmd.OutOrStdout(), "Latest managed title: %s; %s\n", latest.At.Format(time.RFC3339), latest.Title)
				}
				if status.Error != "" {
					fmt.Fprintln(cmd.OutOrStdout(), status.Error)
				}
				return nil
			}
			if enable == disable {
				return fmt.Errorf("pass exactly one of --enable or --disable")
			}
			if background && !enable {
				return fmt.Errorf("--background needs --enable; --disable always removes the background service")
			}
			settings, err := config.LoadSettings()
			if err != nil {
				return err
			}
			if enable {
				if settings.TitleModel == nil || settings.TitleModel.Provider == "" {
					return fmt.Errorf("no title agent configured: run another setup")
				}
				found := false
				for _, id := range settings.EnabledProviders {
					if id == "doubao" {
						found = true
					}
				}
				if !found {
					settings.EnabledProviders = append(settings.EnabledProviders, "doubao")
				}
				if err := validateDoubaoPolicy(settings); err != nil {
					return err
				}
			}
			if enable && (!settings.TitlePolicy.DoubaoEvents || settings.TitlePolicy.DoubaoEnabledAt <= 0) {
				settings.TitlePolicy.DoubaoEnabledAt = time.Now().Unix()
			}
			settings.TitlePolicy.DoubaoEvents = enable
			if err := config.SaveSettings(settings); err != nil {
				return err
			}
			if background {
				executable, err := os.Executable()
				if err != nil {
					return err
				}
				executable, err = filepath.Abs(executable)
				if err != nil {
					return err
				}
				if err := installDoubaoService(executable); err != nil {
					return fmt.Errorf("event policy enabled, but background service installation failed: %w", err)
				}
			}
			if disable {
				if err := removeDoubaoService(); err != nil {
					return fmt.Errorf("event policy disabled, but background service removal failed: %w", err)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Doubao event title policy: %t\n", enable)
			return nil
		},
	}
	doubao.Flags().BoolVar(&enable, "enable", false, "allow automatic native titles on completed-turn events")
	doubao.Flags().BoolVar(&disable, "disable", false, "stop automatic titles")
	doubao.Flags().BoolVar(&background, "background", false, "with --enable, install the long-lived event listener (removed by --disable)")
	doubao.Flags().BoolVar(&watch, "watch", false, "listen for native completed-turn events until stopped")
	cmd.AddCommand(doubao)
	return cmd
}
