// Copyright (c) 2020 XDC.Network

package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/spf13/cast"
	"github.com/BlocksScan/rosetta-XDCNetwork/common"
	"github.com/BlocksScan/rosetta-XDCNetwork/configuration"
	"github.com/BlocksScan/rosetta-XDCNetwork/XDPoSChain"
	XDPoSChaincommon "github.com/XinFinOrg/XDPoSChain/common"
	"github.com/XinFinOrg/XDPoSChain/common/hexutil"
	XDPoSChaintypes "github.com/XinFinOrg/XDPoSChain/core/types"
	"github.com/XinFinOrg/XDPoSChain/crypto"
	"github.com/XinFinOrg/XDPoSChain/rlp"
	"math/big"
	"strconv"
)

type transaction struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Value    *big.Int `json:"value"`
	Input    []byte   `json:"input"`
	Nonce    uint64   `json:"nonce"`
	GasPrice *big.Int `json:"gas_price"`
	GasLimit uint64   `json:"gas"`
	ChainID  *big.Int `json:"chain_id"`
}

// ConstructionAPIService implements the server.ConstructionAPIServicer interface.
type ConstructionAPIService struct {
	config *configuration.Configuration
	client Client
}

// NewConstructionAPIService creates a new instance of a ConstructionAPIService.
func NewConstructionAPIService(
	cfg *configuration.Configuration,
	client Client,
) *ConstructionAPIService {
	return &ConstructionAPIService{
		config: cfg,
		client: client,
	}
}

// ConstructionCombine implements the /construction/combine endpoint.
func (s *ConstructionAPIService) ConstructionCombine(
	ctx context.Context,
	request *types.ConstructionCombineRequest,
) (*types.ConstructionCombineResponse, *types.Error) {
	b, err := hex.DecodeString(request.UnsignedTransaction)
	if err != nil {
		fmt.Println("construction/combine: unable to decode unsigned transaction", err)
		return nil, common.ErrInvalidInputParam
	}
	unsignTx := &transaction{}
	err = rlp.DecodeBytes(b, unsignTx)
	if err != nil {
		fmt.Println("construction/combine: unable to decode unsigned transaction", err)
		return nil, common.ErrInvalidInputParam
	}
	if len(request.Signatures) != 1 {
		fmt.Println("construction/combine: need exact 1 signature", len(request.Signatures))
		return nil, common.ErrInvalidInputParam
	}

	rawSig := request.Signatures[0].Bytes
	if len(rawSig) != 65 {
		fmt.Println("construction/combine: invalid signature length", len(rawSig))
		return nil, common.ErrInvalidInputParam
	}

	chainId, err := strconv.Atoi(request.NetworkIdentifier.Network)
	if err != nil {
		fmt.Println("construction/combine: invalid network", err)
		return nil, common.ErrInvalidInputParam
	}

	XDPoSChainTransaction := XDPoSChaintypes.NewTransaction(
		unsignTx.Nonce,
		XDPoSChaincommon.HexToAddress(unsignTx.To),
		unsignTx.Value,
		unsignTx.GasLimit,
		unsignTx.GasPrice,
		unsignTx.Input,
	)
	signedTx, err := XDPoSChainTransaction.WithSignature(XDPoSChaintypes.NewEIP155Signer(big.NewInt(cast.ToInt64(chainId))), rawSig)
	if err != nil {
		fmt.Println("construction/combine: cannot sign transaction", err)
		return nil, common.ErrServiceInternal
	}
	signedTxData, err := rlp.EncodeToBytes(signedTx)
	if err != nil {
		fmt.Println("construction/combine: cannot encode signed transaction", err)
		return nil, common.ErrServiceInternal
	}
	return &types.ConstructionCombineResponse{
		SignedTransaction: hex.EncodeToString(signedTxData),
	}, nil
}

// ConstructionDerive implements the /construction/derive endpoint.
func (s *ConstructionAPIService) ConstructionDerive(
	ctx context.Context,
	request *types.ConstructionDeriveRequest,
) (*types.ConstructionDeriveResponse, *types.Error) {
	if terr := ValidateNetworkIdentifier(ctx, s.client, request.NetworkIdentifier); terr != nil {
		return nil, terr
	}

	if len(request.PublicKey.Bytes) == 0 || request.PublicKey.CurveType != types.Secp256k1 {
		fmt.Println("/construction/derive: unsupported public key type", request.PublicKey.CurveType, len(request.PublicKey.Bytes))
		return nil, common.ErrInvalidInputParam
	}
	pubkey, err := crypto.DecompressPubkey(request.PublicKey.Bytes)
	if err != nil {
		return nil, common.ErrUnableToDecompressPubkey
	}
	pubBytes := crypto.FromECDSAPub(pubkey)
	addr := XDPoSChain.PubToAddress(pubBytes)

	return &types.ConstructionDeriveResponse{
		AccountIdentifier: &types.AccountIdentifier{
			Address: addr.String(),
		},
	}, nil
}

// ConstructionHash implements the /construction/hash endpoint.
func (s *ConstructionAPIService) ConstructionHash(
	ctx context.Context,
	request *types.ConstructionHashRequest,
) (*types.TransactionIdentifierResponse, *types.Error) {
	if terr := ValidateNetworkIdentifier(ctx, s.client, request.NetworkIdentifier); terr != nil {
		return nil, terr
	}
	tran, err := hex.DecodeString(request.SignedTransaction)
	if err != nil {
		fmt.Println("/construction/hash: invalid signed transaction format", err)
		return nil, common.ErrInvalidInputParam
	}
	tx := &XDPoSChaintypes.Transaction{}
	err = rlp.DecodeBytes(tran, tx)
	if err != nil {
		fmt.Println("/construction/hash: unable to decode signed transaction ", err)
		return nil, common.ErrInvalidInputParam
	}

	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: tx.Hash().String(),
		},
	}, nil
}

// FIXME: required options
// sender (string): address of sender
// to (string): destination address
// gas_limit (uint64) : gas limit of the transaction
// gas_price (uint64): gas price in wei
// value (uint64)
// data ([]bytes) : data include method name, argument if this tx call a contract

func parseMetaDataToCallMsg(options map[string]interface{}) (common.CallArgs, *types.Error) {
	sender, ok := options[common.METADATA_SENDER]
	if !ok {
		fmt.Println("parseMetaDataToCallMsg: empty sender address")
		return common.CallArgs{}, common.ErrInvalidInputParam
	}

	to, ok := options[common.METADATA_RECIPIENT]
	if !ok {
		fmt.Println("parseMetaDataToCallMsg: empty recipient address")
		return common.CallArgs{}, common.ErrInvalidInputParam
	}
	destinationAddress := XDPoSChaincommon.HexToAddress(cast.ToString(to))

	gasLimit, ok := options[common.METADATA_GAS_LIMIT]
	if !ok {
		// set default gaslimit
		gasLimit = common.DefaultGasLimit
	}

	gp, ok := options[common.METADATA_GAS_PRICE]
	if !ok {
		gp = XDPoSChaincommon.DefaultMinGasPrice
	}
	gasPrice, _ := new(big.Int).SetString(cast.ToString(gp), 10)

	v, ok := options[common.METADATA_TRANSACTION_AMOUNT]
	if !ok {
		v = 0
	}
	value, _ := new(big.Int).SetString(cast.ToString(v), 10)

	d, ok := options[common.METADATA_TRANSACTION_DATA]
	if !ok || d == nil {
		d = []byte{}
	}

	callMsg := common.CallArgs{
		From:     XDPoSChaincommon.HexToAddress(cast.ToString(sender)),
		To:       &destinationAddress,
		Gas:      (hexutil.Uint64)(cast.ToUint64(gasLimit)),
		GasPrice: (hexutil.Big)(*gasPrice),
		Value:    (hexutil.Big)(*value),
		Data:     d.([]byte),
	}
	return callMsg, nil
}

// ConstructionMetadata implements the /construction/metadata endpoint.
func (s *ConstructionAPIService) ConstructionMetadata(
	ctx context.Context,
	request *types.ConstructionMetadataRequest,
) (*types.ConstructionMetadataResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, common.ErrUnavailableOffline
	}
	if terr := ValidateNetworkIdentifier(ctx, s.client, request.NetworkIdentifier); terr != nil {
		return nil, terr
	}

	callMsg, terr := parseMetaDataToCallMsg(request.Options)
	if terr != nil {
		return nil, terr
	}
	estimateGas, err := s.client.EstimateGas(ctx, callMsg)
	if err != nil {
		fmt.Println("construction/metadata: failed to estimate gas", err)
		return nil, common.ErrUnableToEstimateGas
	}
	account, err := s.client.Balance(ctx, &types.AccountIdentifier{
		Address: callMsg.From.String(),
	}, nil)
	if err != nil {
		fmt.Println("construction/metadata: failed to getAccount", callMsg.From.String(), err)
		return nil, common.ErrUnableToGetAccount
	}
	meta := account.Metadata

	meta[common.METADATA_GAS_LIMIT] = callMsg.Gas
	meta[common.METADATA_GAS_PRICE] = callMsg.GasPrice.ToInt()
	meta[common.METADATA_SENDER] = callMsg.From

	v, ok := request.Options[common.METADATA_TRANSACTION_AMOUNT]
	if !ok {
		v = 0
	}
	value, _ := new(big.Int).SetString(cast.ToString(v), 10)
	meta[common.METADATA_AMOUNT] = value
	suggestedFee := new(big.Int).Mul(new(big.Int).SetUint64(estimateGas), callMsg.GasPrice.ToInt())
	return &types.ConstructionMetadataResponse{
		Metadata: meta,
		SuggestedFee: []*types.Amount{
			{
				Value:    suggestedFee.String(),
				Currency: common.XDCNativeCoin,
			},
		},
	}, nil
}

// ConstructionParse implements the /construction/parse endpoint.
func (s *ConstructionAPIService) ConstructionParse(
	ctx context.Context,
	request *types.ConstructionParseRequest,
) (*types.ConstructionParseResponse, *types.Error) {
	tx := &transaction{}
	if !request.Signed {
		// decode unsigned transaction
		b, err := hex.DecodeString(request.Transaction)
		if err != nil {
			fmt.Println("construction/parse: failed to decode transaction", err)
			return nil, common.ErrUnableToParseTx
		}
		err = rlp.DecodeBytes(b, tx)
		if err != nil {
			fmt.Println("construction/parse: failed to decode transaction", err)
			return nil, common.ErrUnableToParseTx
		}
	} else {
		// decode signed transaction
		t := new(XDPoSChaintypes.Transaction)
		b, err := hex.DecodeString(request.Transaction)
		if err != nil {
			fmt.Println("construction/parse: failed to decode transaction", err)
			return nil, common.ErrUnableToParseTx
		}
		err = rlp.DecodeBytes(b, t)
		if err != nil {
			fmt.Println("construction/parse: failed to decode transaction", err)
			return nil, common.ErrUnableToParseTx
		}

		tx.To = t.To().String()
		tx.Value = t.Value()
		tx.Input = t.Data()
		tx.Nonce = t.Nonce()
		tx.GasPrice = t.GasPrice()
		tx.GasLimit = t.Gas()
		tx.ChainID = t.ChainId()

		msg, err := t.AsMessage(XDPoSChaintypes.NewEIP155Signer(t.ChainId()), nil, nil)
		if err != nil {
			fmt.Println("construction/parse: unable to GetAccount", err)
			return nil, common.ErrUnableToGetAccount
		}
		tx.From = msg.From().String()
	}

	// Ensure valid from address
	ok := XDPoSChaincommon.IsHexAddress(tx.From)
	if !ok {
		fmt.Printf("construction/parse: %s is not a valid address", tx.From)
		return nil, common.ErrUnableToGetAccount
	}

	// Ensure valid to address
	ok = XDPoSChaincommon.IsHexAddress(tx.From)
	if !ok {
		fmt.Printf("construction/parse: %s is not a valid address", tx.To)
		return nil, common.ErrUnableToGetAccount
	}

	ops := []*types.Operation{
		{
			Type: common.CallOpType,
			OperationIdentifier: &types.OperationIdentifier{
				Index: 0,
			},
			Account: &types.AccountIdentifier{
				Address: tx.From,
			},
			Amount: &types.Amount{
				Value:    new(big.Int).Neg(tx.Value).String(),
				Currency: common.XDCNativeCoin,
			},
		},
		{
			Type: common.CallOpType,
			OperationIdentifier: &types.OperationIdentifier{
				Index: 1,
			},
			RelatedOperations: []*types.OperationIdentifier{
				{
					Index: 0,
				},
			},
			Account: &types.AccountIdentifier{
				Address: tx.To,
			},
			Amount: &types.Amount{
				Value:    tx.Value.String(),
				Currency: common.XDCNativeCoin,
			},
		},
	}

	metaMap := map[string]interface{}{
		common.METADATA_NONCE:     tx.Nonce,
		common.METADATA_GAS_PRICE: tx.GasPrice,
		common.METADATA_CHAIN_ID:  tx.ChainID,
	}
	resp := &types.ConstructionParseResponse{}

	if request.Signed {
		resp = &types.ConstructionParseResponse{
			Operations: ops,
			AccountIdentifierSigners: []*types.AccountIdentifier{
				{
					Address: tx.From,
				},
			},
			Metadata: metaMap,
		}
	} else {
		resp = &types.ConstructionParseResponse{
			Operations:               ops,
			AccountIdentifierSigners: []*types.AccountIdentifier{},
			Metadata:                 metaMap,
		}
	}
	return resp, nil
}

// ConstructionPayloads implements the /construction/payloads endpoint.
func (s *ConstructionAPIService) ConstructionPayloads(
	ctx context.Context,
	request *types.ConstructionPayloadsRequest,
) (*types.ConstructionPayloadsResponse, *types.Error) {
	if len(request.Operations) != 2 {
		fmt.Println("construction/payloads: ConstructionPayloadsRequest require 2 operations", len(request.Operations))
		return nil, common.ErrInvalidInputParam
	}
	addr := request.Operations[0].Account.Address

	nonce, ok := request.Metadata[common.METADATA_ACCOUNT_SEQUENCE]
	if !ok || nonce == nil {
		fmt.Println("construction/payloads: failed to getNextNonce from metadata", addr)
		return nil, common.ErrUnableToGetNextNonce
	}
	txValue, _ := new(big.Int).SetString(cast.ToString(request.Operations[1].Amount.Value), 10)
	gasPrice, _ := new(big.Int).SetString(cast.ToString(request.Metadata[common.METADATA_GAS_PRICE]), 10)
	var txdata []byte
	if request.Metadata[common.METADATA_TRANSACTION_DATA] != nil {
		txdata = request.Metadata[common.METADATA_TRANSACTION_DATA].([]byte)
	}
	tx := XDPoSChaintypes.NewTransaction(cast.ToUint64(nonce),
		XDPoSChaincommon.HexToAddress(request.Operations[1].Account.Address),
		txValue,
		cast.ToUint64(request.Metadata[common.METADATA_GAS_LIMIT]),
		gasPrice,
		txdata)
	checkFrom := request.Operations[0].Account.Address

	// get ChainId from configuration, because ConstructionPayloads is in offline mode
	id := s.config.Network.Network

	chainId := new(big.Int).SetUint64(cast.ToUint64(id))
	unsignedTx := &transaction{
		From:     checkFrom,
		To:       request.Operations[1].Account.Address,
		Value:    tx.Value(),
		Input:    tx.Data(),
		Nonce:    tx.Nonce(),
		GasPrice: gasPrice,
		GasLimit: tx.Gas(),
		ChainID:  chainId,
	}

	d, err := rlp.EncodeToBytes(unsignedTx)
	if err != nil {
		fmt.Println("construction/payloads: failed to encode transaction", err)
		return nil, common.ErrServiceInternal
	}
	unsignedTxEncode := hex.EncodeToString(d)

	signer := XDPoSChaintypes.NewEIP155Signer(chainId)

	return &types.ConstructionPayloadsResponse{
		UnsignedTransaction: unsignedTxEncode,
		Payloads: []*types.SigningPayload{
			{
				AccountIdentifier: &types.AccountIdentifier{
					Address: checkFrom,
				},
				Bytes:         signer.Hash(tx).Bytes(),
				SignatureType: types.EcdsaRecovery,
			},
		},
	}, nil
}

// ConstructionPreprocess implements the /construction/preprocess endpoint.
func (s *ConstructionAPIService) ConstructionPreprocess(
	ctx context.Context,
	request *types.ConstructionPreprocessRequest,
) (*types.ConstructionPreprocessResponse, *types.Error) {
	options := make(map[string]interface{})
	if len(request.Operations) != 2 {
		return nil, common.ErrConstructionCheck
	}
	// sender
	options[common.METADATA_SENDER] = request.Operations[0].Account.Address
	options[common.METADATA_TRANSACTION_TYPE] = request.Operations[0].Type
	options[common.METADATA_SYMBOL] = request.Operations[0].Amount.Currency.Symbol
	options[common.METADATA_DECIMALS] = request.Operations[0].Amount.Currency.Decimals

	// recipient
	options[common.METADATA_RECIPIENT] = request.Operations[1].Account.Address
	options[common.METADATA_AMOUNT] = request.Operations[1].Amount.Value

	if request.Metadata[common.METADATA_GAS_LIMIT] != nil {
		options[common.METADATA_GAS_LIMIT] = request.Metadata[common.METADATA_GAS_LIMIT]
	}
	if request.Metadata[common.METADATA_GAS_PRICE] != nil {
		options[common.METADATA_GAS_PRICE] = request.Metadata[common.METADATA_GAS_PRICE]
	}
	return &types.ConstructionPreprocessResponse{
		Options: options,
	}, nil
}

// ConstructionSubmit implements the /construction/submit endpoint.
func (s *ConstructionAPIService) ConstructionSubmit(
	ctx context.Context,
	request *types.ConstructionSubmitRequest,
) (*types.TransactionIdentifierResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, common.ErrUnavailableOffline
	}
	terr := ValidateNetworkIdentifier(ctx, s.client, request.NetworkIdentifier)
	if terr != nil {
		return nil, terr
	}

	tran, err := hex.DecodeString(request.SignedTransaction)
	if err != nil {
		fmt.Println("construction/submit: failed to decode transaction", err)
		return nil, common.ErrUnableToParseTx
	}

	txID, err := s.client.SubmitTx(ctx, tran)
	if err != nil {
		fmt.Println("construction/submit: failed to submit transaction", err)
		return nil, common.ErrUnableToSubmitTx
	}

	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: txID,
		},
	}, nil
}
