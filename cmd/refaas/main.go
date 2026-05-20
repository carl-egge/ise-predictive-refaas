package main

import (
	_ "github.com/carl-egge/ise-predictive-refaas/internal/builder"
	"github.com/carl-egge/ise-predictive-refaas/internal/service"
	_ "github.com/carl-egge/ise-predictive-refaas/internal/translator"
	log "github.com/sirupsen/logrus"
)

func main() {
	log.SetLevel(log.DebugLevel)
	if err := service.MakeConverterService(); err != nil {
		panic(err)
	}
}
