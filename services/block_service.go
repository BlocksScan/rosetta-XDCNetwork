// Copyright (c) 2020 XDC.Network

package services

import (
	"context"
	"errors"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/BlocksScan/rosetta-XDCNetwork/common"
	"github.com/BlocksScan/rosetta-XDCNetwork/configuration"
	"github.com/BlocksScan/rosetta-XDCNetwork/XDPoSChain"
)

// BlockAPIService implements the server.BlockAPIServicer interface.
type BlockAPIService struct {
	config *configuration.Configuration
	client Client
}

// NewBlockAPIService creates a new instance of a BlockAPIService.
func NewBlockAPIService(
	cfg *configuration.Configuration,
	client Client,
) *BlockAPIService {
	return &BlockAPIService{
		config: cfg,
		client: client,
	}
}

// Block implements the /block endpoint.
func (s *BlockAPIService) Block(
	ctx context.Context,
	request *types.BlockRequest,
) (*types.BlockResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, common.ErrUnavailableOffline
	}

	block, err := s.client.Block(ctx, request.BlockIdentifier)
	if errors.Is(err, XDPoSChain.ErrBlockOrphaned) {
		return nil, common.ErrBlockOrphaned
	}
	if err != nil {
		return nil, common.ErrXDCNotReady
	}

	return &types.BlockResponse{
		Block: block,
	}, nil
}


// BlockTransaction implements the /block/transaction endpoint.
// Note: we don't implement this, since we already return all transactions
// in the /block endpoint reponse above.
func (s *BlockAPIService) BlockTransaction(
	ctx context.Context,
	request *types.BlockTransactionRequest,
) (*types.BlockTransactionResponse, *types.Error) {
	return nil, common.ErrNotImplemented
}
