package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/BlocksScan/rosetta-XDCNetwork/common"
	"log"
	"net/http"
	"time"

	"github.com/BlocksScan/rosetta-XDCNetwork/configuration"
	"github.com/BlocksScan/rosetta-XDCNetwork/XDPoSChain"
	"github.com/BlocksScan/rosetta-XDCNetwork/services"

	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

const (
	// readTimeout is the maximum duration for reading the entire
	// request, including the body.
	readTimeout = 5 * time.Second

	// writeTimeout is the maximum duration before timing out
	// writes of the response. It is reset whenever a new
	// request's header is read.
	writeTimeout = 120 * time.Second

	// idleTimeout is the maximum amount of time to wait for the
	// next request when keep-alives are enabled.
	idleTimeout = 30 * time.Second
)

var (
	runCmd = &cobra.Command{
		Use:   "run",
		Short: "Run XDC-rosetta",
		RunE:  runRunCmd,
	}
)

func runRunCmd(cmd *cobra.Command, args []string) error {
	cfg, err := configuration.LoadConfiguration()
	if err != nil {
		return fmt.Errorf("%w: unable to load configuration", err)
	}

	// The asserter automatically rejects incorrectly formatted
	// requests.
	asserter, err := asserter.NewServer(
		common.OperationTypes,
		common.HistoricalBalanceSupported,
		[]*types.NetworkIdentifier{cfg.Network},
		XDPoSChain.CallMethods,
		false,
	)
	if err != nil {
		return fmt.Errorf("%w: could not initialize server asserter", err)
	}

	// Start required services
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	go handleSignals([]context.CancelFunc{cancel})

	g, ctx := errgroup.WithContext(ctx)

	var client *XDPoSChain.Client
	if cfg.Mode == configuration.Online {
		if !cfg.RemoteXDC {
			g.Go(func() error {
				return XDPoSChain.StartXDC(ctx, cfg.XDCArguments, g)
			})
		}

		var err error
		client, err = XDPoSChain.NewClient(cfg.XDCURL, cfg.Params)
		if err != nil {
			return fmt.Errorf("%w: cannot initialize XDPoSChain client", err)
		}
		defer client.Close()
	}

	router := services.NewBlockchainRouter(cfg, client, asserter)

	loggedRouter := server.LoggerMiddleware(router)
	corsRouter := server.CorsMiddleware(loggedRouter)
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      corsRouter,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}

	g.Go(func() error {
		log.Printf("server listening on port %d", cfg.Port)
		return server.ListenAndServe()
	})

	g.Go(func() error {
		// If we don't shutdown server in errgroup, it will
		// never stop because server.ListenAndServe doesn't
		// take any context.
		<-ctx.Done()

		return server.Shutdown(ctx)
	})

	err = g.Wait()
	if SignalReceived {
		return errors.New("tomchain-rosetta halted")
	}

	return err
}
