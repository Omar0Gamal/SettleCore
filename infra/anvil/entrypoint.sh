#!/usr/bin/env bash
set -euo pipefail

# Deterministic Local Anvil Chain Entrypoint
# Chain ID: 31337, 1-second block time, Cancun EVM hardfork, pre-funded deterministic accounts.
exec anvil \
  --host 0.0.0.0 \
  --port 8545 \
  --chain-id 31337 \
  --block-time 1 \
  --hardfork cancun \
  --accounts 10 \
  --balance 10000 \
  --mnemonic "test test test test test test test test test test test junk"
