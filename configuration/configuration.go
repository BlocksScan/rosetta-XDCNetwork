// Copyright (c) 2020 XDC.Network

package configuration

import (
	"fmt"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/pkg/errors"
	"github.com/spf13/cast"
	"github.com/BlocksScan/rosetta-XDCNetwork/XDPoSChain"
	"github.com/xinfinorg/XDPoSChain/params"
	"math/big"
	"os"
	"strconv"
)

const (
	// Online is when the implementation is permitted
	// to make outbound connections.
	Online Mode = "ONLINE"

	// Offline is when the implementation is not permitted
	// to make outbound connections.
	Offline Mode = "OFFLINE"

	// ModeEnv is the environment variable read
	// to determine mode.
	ModeEnv = "MODE"

	// NetworkEnv is the environment variable
	// read to determine network.
	NetworkEnv = "NETWORK"

	// PortEnv is the environment variable
	// read to determine the port for the Rosetta
	// implementation.
	PortEnv = "PORT"

	// XDCEnv is an optional environment variable
	// used to connect XDC-rosetta to an already
	// running XDC node.
	XDCEnv = "XDC"

	// DefaultXDCURL is the default URL for
	// a running geth node. This is used
	// when GethEnv is not populated.
	DefaultXDCURL = "http://localhost:8545"

	// Mainnet is the XDC Mainnet.
	Mainnet string = "MAINNET"

	// Testnet is XDC Public Apothem Testnet
	Testnet string = "TESTNET"

	// Devnet is XDC network for development
	Devnet string = "DEVNET"
)

var (
	// MiddlewareVersion is the version of XDC-rosetta.
	MiddlewareVersion = "0.0.2"
)

type Mode string

// Configuration determines how
type Configuration struct {
	Mode                   Mode
	Network                *types.NetworkIdentifier
	GenesisBlockIdentifier *types.BlockIdentifier
	XDCURL                string
	RemoteXDC             bool
	Port                   int
	XDCArguments          string

	Params *params.ChainConfig
}

// LoadConfiguration attempts to create a new Configuration
// using the ENVs in the environment.
func LoadConfiguration() (*Configuration, error) {
	config := &Configuration{}

	modeValue := Mode(os.Getenv(ModeEnv))
	switch modeValue {
	case Online:
		config.Mode = Online
	case Offline:
		config.Mode = Offline
	case "":
		return nil, errors.New("MODE must be populated")
	default:
		return nil, fmt.Errorf("%s is not a valid mode", modeValue)
	}

	networkValue := os.Getenv(NetworkEnv)
	switch networkValue {
	case Mainnet:
		config.Network = &types.NetworkIdentifier{
			Blockchain: XDPoSChain.Blockchain,
			Network:    XDPoSChain.MainnetNetwork,
		}
		config.GenesisBlockIdentifier = XDPoSChain.MainnetGenesisBlockIdentifier
		config.Params = params.XDCMainnetChainConfig
		config.XDCArguments = XDPoSChain.MainnetXDCArguments
	case Testnet:
		config.Network = &types.NetworkIdentifier{
			Blockchain: XDPoSChain.Blockchain,
			Network:    XDPoSChain.TestnetNetwork,
		}
		config.GenesisBlockIdentifier = &types.BlockIdentifier{
			Hash: "",
			Index: XDPoSChain.GenesisBlockIndex,
		}
		testnetChainConfig := params.XDCMainnetChainConfig
		testnetChainConfig.ChainId = new(big.Int).SetUint64(cast.ToUint64(XDPoSChain.TestnetNetwork))
		config.Params = testnetChainConfig
		config.XDCArguments = XDPoSChain.TestnetXDCArguments
	case Devnet:
		config.Network = &types.NetworkIdentifier{
			Blockchain: XDPoSChain.Blockchain,
			Network:    XDPoSChain.DevnetNetwork,
		}
		config.GenesisBlockIdentifier = &types.BlockIdentifier{
			Hash: "",
			Index: XDPoSChain.GenesisBlockIndex,
		}
		devnetChainConfig := params.XDCMainnetChainConfig
		devnetChainConfig.ChainId = new(big.Int).SetUint64(cast.ToUint64(XDPoSChain.DevnetNetwork))
		config.Params = devnetChainConfig
		config.XDCArguments = XDPoSChain.DevnetXDCArguments
	case "":
		return nil, errors.New("NETWORK must be populated")
	default:
		return nil, fmt.Errorf("%s is not a valid network", networkValue)
	}

	config.XDCURL = DefaultXDCURL
	envGethURL := os.Getenv(XDCEnv)
	if len(envGethURL) > 0 {
		config.RemoteXDC = true
		config.XDCURL = envGethURL
	}

	portValue := os.Getenv(PortEnv)
	if len(portValue) == 0 {
		return nil, errors.New("PORT must be populated")
	}

	port, err := strconv.Atoi(portValue)
	if err != nil || len(portValue) == 0 || port <= 0 {
		return nil, fmt.Errorf("%w: unable to parse port %s", err, portValue)
	}
	config.Port = port

	return config, nil
}
