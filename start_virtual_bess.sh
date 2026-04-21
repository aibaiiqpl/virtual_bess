#!/bin/bash

APP="/opt/mock_emu/virtual_bess"

CFG1="/opt/mock_emu/app1.yaml"
CFG2="/opt/mock_emu/app2.yaml"

trap 'echo "[INFO] Stopping..."; kill $PID1 $PID2' SIGTERM SIGINT

$APP -config "$CFG1" &
PID1=$!

$APP -config "$CFG2" &
PID2=$!

wait
