package main

import (
	"fmt"
	"os"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/thanos-io/thanos/pkg/component"
	grpcserver "github.com/thanos-io/thanos/pkg/server/grpc"
	"github.com/thanos-io/thanos/pkg/tls"
	"github.com/thanos-io/thanos/pkg/tracing/client"
	"github.com/u2takey/tencentcloud-thanos-querier/config"
	"gopkg.in/alecthomas/kingpin.v2"

	store "github.com/u2takey/tencentcloud-thanos-querier/pkg"
)

func registerStore(m map[string]setupFunc, app *kingpin.Application) {
	comp := component.Store
	cmd := app.Command(comp.String(), "store adapter for TencentCloud Metrics")

	grpcBindAddr, grpcGracePeriod, grpcCert, grpcKey, grpcClientCA := regGRPCFlags(cmd)

	configFile := cmd.Flag("config.file", "TencentCloud metrics store configuration file.").
		Default("qcloud.yml").String()

	m[comp.String()] = func(g *run.Group, logger log.Logger, reg *prometheus.Registry, _ bool) error {

		return runStore(
			g,
			logger,
			reg,
			*grpcBindAddr,
			time.Duration(*grpcGracePeriod),
			*grpcCert,
			*grpcKey,
			*grpcClientCA,
			*configFile,
		)
	}
}

func runStore(
	g *run.Group,
	logger log.Logger,
	reg *prometheus.Registry,
	grpcBindAddr string,
	grpcGracePeriod time.Duration,
	grpcCert string,
	grpcKey string,
	grpcClientCA string,
	configFile string,
) error {

	tConfig := config.NewConfig()
	if err := tConfig.LoadFile(configFile); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, errors.Wrapf(err, "Error parsing config.files"))
		os.Exit(2)
	}
	promStore, err := store.NewTencentCloudStore(logger, tConfig, component.Store)
	if err != nil {
		return errors.Wrap(err, "create TencentCloud Metrics store")
	}

	tlsCfg, err := tls.NewServerConfig(log.With(logger, "protocol", "gRPC"), grpcCert, grpcKey, grpcClientCA)
	if err != nil {
		return errors.Wrap(err, "setup gRPC server")
	}

	tracer := client.NoopTracer()

	s := grpcserver.New(logger, reg, tracer, component.Store, promStore,
		grpcserver.WithListen(grpcBindAddr),
		grpcserver.WithGracePeriod(grpcGracePeriod),
		grpcserver.WithTLSConfig(tlsCfg),
	)
	g.Add(func() error {
		return s.ListenAndServe()
	}, func(err error) {
		s.Shutdown(err)
	})

	_ = level.Info(logger).Log("msg", "starting querier")
	return nil
}
