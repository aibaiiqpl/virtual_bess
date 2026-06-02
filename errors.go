package main

import "fmt"

func errUnknownSlaveID(slaveID uint8) error {
	return fmt.Errorf("slave_id %d not found", slaveID)
}
