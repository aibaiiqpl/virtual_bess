package client_sg

import (
	"testing"

	"github.com/go-bindings/iec61850"
	"github.com/go-bindings/iec61850/test"
)

func TestWriteSG(t *testing.T) {
	client := test.CreateClient(t)
	defer test.CloseClient(client)

	if err := client.WriteSG("DEMOPROT", "LLN0", "DEMOPROT/PTOC1.StrVal.setMag.f", iec61850.SE, 2, float32(1.0)); err != nil {
		t.Fatalf("write setting group error %v\n", err)
	}
}

func TestGetSG(t *testing.T) {
	client := test.CreateClient(t)
	defer test.CloseClient(client)

	sgInfo, err := client.GetSG("DEMOPROT/LLN0.SGCB")
	if err != nil {
		t.Fatalf("get setting group error %v\n", err)
	}

	t.Logf("setting group info %#v\n", sgInfo)
}
