package server

import (
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/go-bindings/iec61850"
)

const (
	AnIn1ObjectRef = "simpleIOGenericIO/GGIO1.AnIn1"
)

func TestCreateServerFromConfigFile3(t *testing.T) {
	model, err := iec61850.CreateModelFromConfigFileEx("simpleIO_direct_control_goose.cfg")
	if err != nil {
		t.Fatalf("create model error %v\n", err)
	}

	server := iec61850.NewServerWithConfig(iec61850.NewServerConfig(), model)
	server.Start(102)

	ticker := time.NewTicker(time.Second)
	go func() {
		defer ticker.Stop()

		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		minVal, maxVal := float32(10.0), float32(10000.0)

		for range ticker.C {
			node := model.GetModelNodeByObjectReference(AnIn1ObjectRef + ".mag.f")
			fValue := r.Float32()*(maxVal-minVal) + minVal

			server.LockDataModel()
			server.UpdateFloatAttributeValue(node, fValue)
			server.UpdateUTCTimeAttributeValue(model.GetModelNodeByObjectReference(AnIn1ObjectRef+".t"), time.Now().UnixMilli())
			server.UnlockDataModel()
		}
	}()
	defer server.Destroy()
	t.Logf("Server start up\n")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	t.Logf("Server stop\n")
	server.Stop()
}
