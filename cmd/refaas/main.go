package main

import (
	_ "github.com/carl-egge/ise-predictive-refaas/internal/builder"
	"github.com/carl-egge/ise-predictive-refaas/internal/service"
	_ "github.com/carl-egge/ise-predictive-refaas/internal/translator"
	log "github.com/sirupsen/logrus"
)

func main() {
	// Set log level to debug for detailed output during development
	log.SetLevel(log.DebugLevel)

	// Initialize the service (which sets up the HTTP server and handlers)
	log.Info("Starting the ReFaaS 2.0 service ...")
	if err := service.MakeConverterService(); err != nil {
		panic(err)
	}
}
