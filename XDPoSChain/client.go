// Copyright (c) 2020 XDC.Network

package XDPoSChain

import (
	"context"
	"encoding/json"
	"fmt"
	RosettaTypes "github.com/coinbase/rosetta-sdk-go/types"
	"github.com/spf13/cast"
	"github.com/XinFinOrg/XDPoSChain"
	"github.com/BlocksScan/rosetta-XDCNetwork/common"
	XDPoSChaincommon "github.com/XinFinOrg/XDPoSChain/common"
	"github.com/XinFinOrg/XDPoSChain/common/hexutil"
	"github.com/XinFinOrg/XDPoSChain/consensus/XDPoS"
	XDPoSChaintypes "github.com/XinFinOrg/XDPoSChain/core/types"
	"github.com/XinFinOrg/XDPoSChain/crypto"
	"github.com/XinFinOrg/XDPoSChain/eth"
	"github.com/XinFinOrg/XDPoSChain/p2p"
	"github.com/XinFinOrg/XDPoSChain/params"
	"github.com/XinFinOrg/XDPoSChain/rpc"
	"golang.org/x/sync/semaphore"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	GenesisBlockIndex    = int64(0)
	maxTraceConcurrency  = int64(16)
	semaphoreTraceWeight = int64(1)
	XDCHTTPTimeout      = 120 * time.Second
)

type (
	// Client is an implementation of XDPoSChain client using local rpc/ipc connection.
	Client struct {
		sync.RWMutex
		tracerConfig   *eth.TraceConfig
		traceSemaphore *semaphore.Weighted
		c              *rpc.Client
		p              *params.ChainConfig
	}
)

// cache chainId to avoid spam rpc
var chainId *big.Int

func NewClient(url string, params *params.ChainConfig) (cli *Client, err error) {
	rpcClient, err := rpc.DialHTTPWithClient(url, &http.Client{
		Timeout: XDCHTTPTimeout,
	})
	if err != nil {
		return nil, err
	}
	tracerConfig, err := loadTraceConfig()
	if err != nil {
		return nil, fmt.Errorf("%w: unable to load trace config", err)
	}
	return &Client{
		c:              rpcClient,
		tracerConfig:   tracerConfig,
		traceSemaphore: semaphore.NewWeighted(maxTraceConcurrency),
		p:              params,
	}, nil
}

// Close shuts down the RPC client connection.
func (tc *Client) Close() {
	tc.c.Close()
}

// PendingNonceAt returns the account nonce of the given account in the state.
// This is the nonce that should be used for the next transaction.
func (tc *Client) PendingNonceAt(ctx context.Context, account XDPoSChaincommon.Address) (uint64, error) {
	var result hexutil.Uint64
	err := tc.c.CallContext(ctx, &result, common.RPC_METHOD_GET_TRANSACTION_COUNT, account, toBlockNumArg(big.NewInt(-1)))
	return uint64(result), err
}

// NonceAt returns the account nonce of the given account in the state.
// This is the nonce that should be used for the next transaction.
func (tc *Client) NonceAt(ctx context.Context, account XDPoSChaincommon.Address, blockNumber string) (uint64, error) {
	var result hexutil.Uint64
	err := tc.c.CallContext(ctx, &result, common.RPC_METHOD_GET_TRANSACTION_COUNT, account, blockNumber)
	return uint64(result), err
}


// SuggestGasPrice retrieves the currently suggested gas price to allow a timely
// execution of a transaction.
func (tc *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	var result hexutil.Uint64
	if err := tc.c.CallContext(ctx, &result, common.RPC_METHOD_GAS_PRICE); err != nil {
		return new(big.Int).SetUint64(0), err
	}
	return new(big.Int).SetUint64(uint64(result)), nil
}

// Peers retrieves all peers of the node.
func (tc *Client) peers(ctx context.Context) ([]*RosettaTypes.Peer, error) {
	var info []*p2p.PeerInfo
	if err := tc.c.CallContext(ctx, &info, "admin_peers"); err != nil {
		return nil, err
	}

	peers := make([]*RosettaTypes.Peer, len(info))
	for i, peerInfo := range info {
		peers[i] = &RosettaTypes.Peer{
			PeerID: peerInfo.ID,
			Metadata: map[string]interface{}{
				"name":      peerInfo.Name,
				"caps":      peerInfo.Caps,
				"protocols": peerInfo.Protocols,
			},
		}
	}

	return peers, nil
}

func (tc *Client) GetChainID(ctx context.Context) (*big.Int, error) {
	if chainId == nil {
		id := new(hexutil.Uint64)
		err := tc.c.CallContext(ctx, &id, common.RPC_METHOD_GET_CHAIN_ID)
		if err != nil {
			return nil, err
		}
		chainId = new(big.Int).SetUint64(uint64(*id))
	}
	return chainId, nil
}

// Block returns a populated block at the *RosettaTypes.PartialBlockIdentifier.
// If neither the hash or index is populated in the *RosettaTypes.PartialBlockIdentifier,
// the current block is returned.
func (tc *Client) Block(
	ctx context.Context,
	blockIdentifier *RosettaTypes.PartialBlockIdentifier,
) (*RosettaTypes.Block, error) {
	if blockIdentifier != nil {
		if blockIdentifier.Hash != nil {
			return tc.getParsedBlock(ctx, common.RPC_METHOD_GET_BLOCK_BY_HASH, *blockIdentifier.Hash, true)
		}

		if blockIdentifier.Index != nil {
			return tc.getParsedBlock(
				ctx,
				common.RPC_METHOD_GET_BLOCK_BY_NUMBER,
				toBlockNumArg(big.NewInt(*blockIdentifier.Index)),
				true,
			)
		}
	}

	return tc.getParsedBlock(ctx, common.RPC_METHOD_GET_BLOCK_BY_NUMBER, toBlockNumArg(nil), true)
}

func (tc *Client) getUncles(
	ctx context.Context,
	head *XDPoSChaintypes.Header,
	body *rpcBlock,
) ([]*XDPoSChaintypes.Header, error) {
	// Quick-verify transaction and uncle lists. This mostly helps with debugging the server.
	if head.UncleHash == XDPoSChaintypes.EmptyUncleHash && len(body.UncleHashes) > 0 {
		return nil, fmt.Errorf(
			"server returned non-empty uncle list but block header indicates no uncles",
		)
	}
	if head.UncleHash != XDPoSChaintypes.EmptyUncleHash && len(body.UncleHashes) == 0 {
		return nil, fmt.Errorf(
			"server returned empty uncle list but block header indicates uncles",
		)
	}
	if head.TxHash == XDPoSChaintypes.EmptyRootHash && len(body.Transactions) > 0 {
		return nil, fmt.Errorf(
			"server returned non-empty transaction list but block header indicates no transactions",
		)
	}
	if head.TxHash != XDPoSChaintypes.EmptyRootHash && len(body.Transactions) == 0 {
		return nil, fmt.Errorf(
			"server returned empty transaction list but block header indicates transactions",
		)
	}
	// Load uncles because they are not included in the block response.
	var uncles []*XDPoSChaintypes.Header
	if len(body.UncleHashes) > 0 {
		uncles = make([]*XDPoSChaintypes.Header, len(body.UncleHashes))
		reqs := make([]rpc.BatchElem, len(body.UncleHashes))
		for i := range reqs {
			reqs[i] = rpc.BatchElem{
				Method: "eth_getUncleByBlockHashAndIndex",
				Args:   []interface{}{body.Hash, hexutil.EncodeUint64(uint64(i))},
				Result: &uncles[i],
			}
		}
		if err := tc.c.BatchCallContext(ctx, reqs); err != nil {
			return nil, err
		}
		for i := range reqs {
			if reqs[i].Error != nil {
				return nil, reqs[i].Error
			}
			if uncles[i] == nil {
				return nil, fmt.Errorf(
					"got null header for uncle %d of block %x",
					i,
					body.Hash[:],
				)
			}
		}
	}

	return uncles, nil
}

func (tc *Client) getBlock(
	ctx context.Context,
	blockMethod string,
	args ...interface{},
) (
	*XDPoSChaintypes.Block,
	[]*loadedTransaction,
	string,
	error,
) {
	var raw json.RawMessage
	err := tc.c.CallContext(ctx, &raw, blockMethod, args...)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: block fetch failed", err)
	} else if len(raw) == 0 {
		return nil, nil, "", XDPoSChain.NotFound
	}

	var data map[string]interface{}
	if err = json.Unmarshal(raw, &data); err != nil {
		fmt.Println("getBlockByNumber error when unmarshalling raw data")
		return nil, []*loadedTransaction{}, "", err
	}
	// include M2 signature
	//FIXME: XDPoSChain Blockchain includes double validation mechanism
	// so block.Hash() won't response the correct blockhash
	// remember to get 'hash' field from response data

	finalBlockHash := ""
	if data["hash"] != nil {
		finalBlockHash = (data["hash"]).(string)
	}

	// Decode header and transactions
	var head XDPoSChaintypes.Header
	var body rpcBlock
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, nil, "", err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, nil, "", err
	}

	uncles, err := tc.getUncles(ctx, &head, &body)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: unable to get uncles", err)
	}

	// Get all transaction receipts
	receipts, err := tc.getBlockReceipts(ctx, body.Hash, body.Transactions)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: could not get receipts for %x", err, body.Hash[:])
	}

	miner := XDPoSChaincommon.Address{}

	// Get block traces (not possible to make idempotent block transaction trace requests)
	//
	// We fetch traces last because we want to avoid limiting the number of other
	// block-related data fetches we perform concurrently (we limit the number of
	// concurrent traces that are computed to 16 to avoid overwhelming geth).
	var addTraces bool
	if head.Number.Int64() != GenesisBlockIndex { // not possible to get traces at genesis
		addTraces = true
		miner, err = GetCoinbaseFromHeader(&head)
		if err != nil {
			fmt.Println("Failed to get miner of block", head.Number, finalBlockHash)
			return nil, nil, "", err
		}
	}

	// Convert all txs to loaded txs
	txs := make([]*XDPoSChaintypes.Transaction, len(body.Transactions))
	loadedTxs := make([]*loadedTransaction, len(body.Transactions))
	for i, tx := range body.Transactions {
		txs[i] = tx.tx
		receipt := receipts[i]
		gasUsedBig := new(big.Int).SetUint64(receipt.GasUsed)
		feeAmount := gasUsedBig.Mul(gasUsedBig, txs[i].GasPrice())

		loadedTxs[i] = tx.LoadedTransaction()
		loadedTxs[i].Transaction = txs[i]
		loadedTxs[i].FeeAmount = feeAmount

		// tx fee send to masternode owner since hardford common/common.go:22
		if head.Number.Cmp(common.HardForkUpdateTxFee) < 0 {
			loadedTxs[i].Miner = MustChecksum(miner.Hex())
		} else {
			var owner string
			err := tc.c.CallContext(ctx, &owner, common.RPC_METHOD_GET_OWNER_BY_COINBASE, miner, toBlockNumArg(head.Number))
			if err != nil {
				fmt.Println("Failed to get masternode owner of coinbase", head.Number, miner)
				return nil, nil, "", err
			}
			loadedTxs[i].Miner = MustChecksum(owner)
		}

		loadedTxs[i].Receipt = receipt

		// Continue if calls does not exist (occurs at genesis)
		if !addTraces {
			continue
		}
		trace, rawTrace, err := tc.getTransactionTraces(ctx, tx.tx.Hash())
		if err != nil {
			return nil, nil, "", fmt.Errorf("%w: could not get transaction traces for %s", err, tx.tx.Hash().String())
		}
		loadedTxs[i].Trace = trace
		loadedTxs[i].RawTrace = rawTrace
	}

	return XDPoSChaintypes.NewBlockWithHeader(&head).WithBody(txs, uncles), loadedTxs, finalBlockHash, nil
}

func (tc *Client) getBlockTraces(
	ctx context.Context,
	blockHash XDPoSChaincommon.Hash,
) ([]*rpcCall, []*rpcRawCall, error) {
	if err := tc.traceSemaphore.Acquire(ctx, semaphoreTraceWeight); err != nil {
		return nil, nil, err
	}
	defer tc.traceSemaphore.Release(semaphoreTraceWeight)

	var calls []*rpcCall
	var rawCalls []*rpcRawCall
	var raw json.RawMessage
	err := tc.c.CallContext(ctx, &raw, common.RPC_METHOD_DEBUG_TRACE_BLOCK, blockHash, tc.tracerConfig)
	if err != nil {
		return nil, nil, err
	}

	// Decode []*rpcCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, nil, err
	}

	// Decode []*rpcRawCall
	if err := json.Unmarshal(raw, &rawCalls); err != nil {
		return nil, nil, err
	}

	return calls, rawCalls, nil
}

func (tc *Client) getTransactionTraces(
	ctx context.Context,
	txHash XDPoSChaincommon.Hash,
) (*Call, json.RawMessage, error) {
	if err := tc.traceSemaphore.Acquire(ctx, semaphoreTraceWeight); err != nil {
		return nil, nil, err
	}
	defer tc.traceSemaphore.Release(semaphoreTraceWeight)

	var calls *Call
	var rawCalls json.RawMessage
	var raw json.RawMessage
	err := tc.c.CallContext(ctx, &raw, common.RPC_METHOD_DEBUG_TRACE_TRANSACTION, txHash, tc.tracerConfig)
	if err != nil {
		return nil, nil, err
	}

	// Decode *Call
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, nil, err
	}

	// Decode json.RawMessage
	if err := json.Unmarshal(raw, &rawCalls); err != nil {
		return nil, nil, err
	}

	return calls, rawCalls, nil
}

// Header returns a block header from the current canonical chain. If number is
// nil, the latest known header is returned.
func (tc *Client) blockHeader(ctx context.Context, number *big.Int) (*XDPoSChaintypes.Header, error) {
	var head *XDPoSChaintypes.Header
	err := tc.c.CallContext(ctx, &head, common.RPC_METHOD_GET_BLOCK_BY_NUMBER, toBlockNumArg(number), false)
	if err == nil && head == nil {
		return nil, XDPoSChain.NotFound
	}

	return head, err
}

func (tc *Client) GetLatestBlock(ctx context.Context) (*RosettaTypes.Block, error) {
	return tc.Block(ctx, nil)
}

func (tc *Client) GetGenesisBlock(ctx context.Context) (*RosettaTypes.Block, error) {
	index := GenesisBlockIndex
	return tc.Block(ctx, &RosettaTypes.PartialBlockIdentifier{
		Index: &index,
	})
}

func (tc *Client) GetBlockByNumber(ctx context.Context, number *big.Int) (*RosettaTypes.Block, error) {
	if number == nil {
		return tc.GetLatestBlock(ctx)
	}
	index := number.Int64()
	return tc.Block(ctx, &RosettaTypes.PartialBlockIdentifier{
		Index: &index,
	})
}

func (tc *Client) GetBlockByHash(ctx context.Context, hash XDPoSChaincommon.Hash) (*RosettaTypes.Block, error) {
	hashString := hash.Hex()
	return tc.Block(ctx, &RosettaTypes.PartialBlockIdentifier{
		Hash: &hashString,
	})
}

func (tc *Client) getBlockReceipts(
	ctx context.Context,
	blockHash XDPoSChaincommon.Hash,
	txs []rpcTransaction,
) ([]*XDPoSChaintypes.Receipt, error) {
	receipts := make([]*XDPoSChaintypes.Receipt, len(txs))
	if len(txs) == 0 {
		return receipts, nil
	}

	reqs := make([]rpc.BatchElem, len(txs))
	for i := range reqs {
		reqs[i] = rpc.BatchElem{
			Method: common.RPC_METHOD_GET_TRANSACTION_RECEIPT,
			Args:   []interface{}{txs[i].tx.Hash().Hex()},
			Result: &receipts[i],
		}
	}
	if err := tc.c.BatchCallContext(ctx, reqs); err != nil {
		return nil, err
	}
	for i := range reqs {
		if reqs[i].Error != nil {
			return nil, reqs[i].Error
		}
		if receipts[i] == nil {
			return nil, fmt.Errorf("got empty receipt for %x", txs[i].tx.Hash().Hex())
		}
	}

	return receipts, nil
}

// flattenTraces recursively flattens all traces.
func flattenTraces(data *Call, flattened []*flatCall) []*flatCall {
	results := append(flattened, data.flatten())
	for _, child := range data.Calls {
		// Ensure all children of a reverted call
		// are also reverted!
		if data.Revert {
			child.Revert = true

			// Copy error message from parent
			// if child does not have one
			if len(child.ErrorMessage) == 0 {
				child.ErrorMessage = data.ErrorMessage
			}
		}

		children := flattenTraces(child, flattened)
		results = append(results, children...)
	}
	return results
}

// traceOps returns all *RosettaTypes.Operation for a given
// array of flattened traces.
func traceOps(calls []*flatCall, startIndex int) []*RosettaTypes.Operation { // nolint: gocognit
	var ops []*RosettaTypes.Operation
	if len(calls) == 0 {
		return ops
	}

	destroyedAccounts := map[string]*big.Int{}
	for _, trace := range calls {
		// Handle partial transaction success
		metadata := map[string]interface{}{}
		opStatus := common.SUCCESS
		if trace.Revert {
			opStatus = common.FAIL
			metadata["error"] = trace.ErrorMessage
		}

		var zeroValue bool
		if trace.Value.Sign() == 0 {
			zeroValue = true
		}

		// Skip all 0 value CallType operations (TODO: make optional to include)
		//
		// We can't continue here because we may need to adjust our destroyed
		// accounts map if a CallTYpe operation resurrects an account.
		shouldAdd := true
		if zeroValue && CallType(trace.Type) {
			shouldAdd = false
		}

		// Checksum addresses
		from := MustChecksum(trace.From.String())
		to := MustChecksum(trace.To.String())

		if shouldAdd {
			fromOp := &RosettaTypes.Operation{
				OperationIdentifier: &RosettaTypes.OperationIdentifier{
					Index: int64(len(ops) + startIndex),
				},
				Type:   trace.Type,
				Status: &opStatus,
				Account: &RosettaTypes.AccountIdentifier{
					Address: from,
				},
				Amount: &RosettaTypes.Amount{
					Value:    new(big.Int).Neg(trace.Value).String(),
					Currency: common.XDCNativeCoin,
				},
				Metadata: metadata,
			}
			if zeroValue {
				fromOp.Amount = nil
			} else {
				_, destroyed := destroyedAccounts[from]
				if destroyed && opStatus == common.SUCCESS {
					destroyedAccounts[from] = new(big.Int).Sub(destroyedAccounts[from], trace.Value)
				}
			}

			ops = append(ops, fromOp)
		}

		// Add to destroyed accounts if SELFDESTRUCT
		// and overwrite existing balance.
		if trace.Type == common.SelfDestructOpType {
			destroyedAccounts[from] = new(big.Int)

			// If destination of of SELFDESTRUCT is self,
			// we should skip. In the EVM, the balance is reset
			// after the balance is increased on the destination
			// so this is a no-op.
			if from == to {
				continue
			}
		}

		// Skip empty to addresses (this may not
		// actually occur but leaving it as a
		// sanity check)
		if len(trace.To.String()) == 0 {
			continue
		}

		// If the account is resurrected, we remove it from
		// the destroyed accounts map.
		if CreateType(trace.Type) {
			delete(destroyedAccounts, to)
		}

		if shouldAdd {
			lastOpIndex := ops[len(ops)-1].OperationIdentifier.Index
			toOp := &RosettaTypes.Operation{
				OperationIdentifier: &RosettaTypes.OperationIdentifier{
					Index: lastOpIndex + 1,
				},
				RelatedOperations: []*RosettaTypes.OperationIdentifier{
					{
						Index: lastOpIndex,
					},
				},
				Type:   trace.Type,
				Status: &opStatus,
				Account: &RosettaTypes.AccountIdentifier{
					Address: to,
				},
				Amount: &RosettaTypes.Amount{
					Value:    trace.Value.String(),
					Currency: common.XDCNativeCoin,
				},
				Metadata: metadata,
			}
			if zeroValue {
				toOp.Amount = nil
			} else {
				_, destroyed := destroyedAccounts[to]
				if destroyed && opStatus == common.SUCCESS {
					destroyedAccounts[to] = new(big.Int).Add(destroyedAccounts[to], trace.Value)
				}
			}

			ops = append(ops, toOp)
		}
	}
	// Zero-out all destroyed accounts that are removed
	// during transaction finalization.
	for acct, val := range destroyedAccounts {
		if val.Sign() == 0 {
			continue
		}

		if val.Sign() < 0 {
			log.Fatalf("negative balance for suicided account %s: %s\n", acct, val.String())
		}

		ops = append(ops, &RosettaTypes.Operation{
			OperationIdentifier: &RosettaTypes.OperationIdentifier{
				Index: ops[len(ops)-1].OperationIdentifier.Index + 1,
			},
			Type:   common.DestructOpType,
			Status: &common.SUCCESS,
			Account: &RosettaTypes.AccountIdentifier{
				Address: acct,
			},
			Amount: &RosettaTypes.Amount{
				Value:    new(big.Int).Neg(val).String(),
				Currency: common.XDCNativeCoin,
			},
		})
	}

	return ops
}

func feeOps(tx *loadedTransaction) []*RosettaTypes.Operation {
	return []*RosettaTypes.Operation{
		{
			OperationIdentifier: &RosettaTypes.OperationIdentifier{
				Index: 0,
			},
			Type:   common.FeeOpType,
			Status: &common.SUCCESS,
			Account: &RosettaTypes.AccountIdentifier{
				Address: MustChecksum(tx.From.String()),
			},
			Amount: &RosettaTypes.Amount{
				Value:    new(big.Int).Neg(tx.FeeAmount).String(),
				Currency: common.XDCNativeCoin,
			},
		},

		{
			OperationIdentifier: &RosettaTypes.OperationIdentifier{
				Index: 1,
			},
			RelatedOperations: []*RosettaTypes.OperationIdentifier{
				{
					Index: 0,
				},
			},
			Type:   common.FeeOpType,
			Status: &common.SUCCESS,
			Account: &RosettaTypes.AccountIdentifier{
				Address: MustChecksum(tx.Miner),
			},
			Amount: &RosettaTypes.Amount{
				Value:    tx.FeeAmount.String(),
				Currency: common.XDCNativeCoin,
			},
		},
	}
}

// transactionReceipt returns the receipt of a transaction by transaction hash.
// Note that the receipt is not available for pending transactions.
func (tc *Client) transactionReceipt(
	ctx context.Context,
	txHash XDPoSChaincommon.Hash,
) (*XDPoSChaintypes.Receipt, error) {
	var r *XDPoSChaintypes.Receipt
	err := tc.c.CallContext(ctx, &r, common.RPC_METHOD_GET_TRANSACTION_RECEIPT, txHash)
	if err == nil {
		if r == nil {
			return nil, XDPoSChain.NotFound
		}
	}

	return r, err
}

func (tc *Client) getParsedBlock(
	ctx context.Context,
	blockMethod string,
	args ...interface{},
) (
	*RosettaTypes.Block,
	error,
) {
	block, loadedTransactions, finalBlockHash, err := tc.getBlock(ctx, blockMethod, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: could not get block", err)
	}

	blockIdentifier := &RosettaTypes.BlockIdentifier{
		Hash:  finalBlockHash,
		Index: block.Number().Int64(),
	}

	parentBlockIdentifier := blockIdentifier
	if blockIdentifier.Index != GenesisBlockIndex {
		parentBlockIdentifier = &RosettaTypes.BlockIdentifier{
			Hash:  block.ParentHash().Hex(),
			Index: blockIdentifier.Index - 1,
		}
	} else {
		// genesis block
		// following https://www.rosetta-api.org/docs/common_mistakes.html#malformed-genesis-block
		// parentBlock == genesisBlock
		parentBlockIdentifier = &RosettaTypes.BlockIdentifier{
			Index: GenesisBlockIndex,
			Hash:  finalBlockHash,
		}
	}

	txs, err := tc.populateTransactions(ctx, blockIdentifier, block, loadedTransactions)
	if err != nil {
		return nil, err
	}

	return &RosettaTypes.Block{
		BlockIdentifier:       blockIdentifier,
		ParentBlockIdentifier: parentBlockIdentifier,
		Timestamp:             convertTime(block.Time().Uint64()),
		Transactions:          txs,
	}, nil
}

func convertTime(time uint64) int64 {
	return int64(time) * 1000
}

func (tc *Client) populateTransactions(
	ctx context.Context,
	blockIdentifier *RosettaTypes.BlockIdentifier,
	block *XDPoSChaintypes.Block,
	loadedTransactions []*loadedTransaction,
) ([]*RosettaTypes.Transaction, error) {
	var (
		transactions []*RosettaTypes.Transaction
		rewardTx     *RosettaTypes.Transaction
		err          error
	)
	// Compute reward transaction (block + uncle reward)
	if block.NumberU64()%common.Epoch == 0 && block.NumberU64() > 0 {
		rewardTx, err = tc.populateRewardTransaction(ctx, blockIdentifier)
		if err != nil {
			return []*RosettaTypes.Transaction{}, nil
		}
	}

	index := uint64(0)
	if rewardTx != nil {
		transactions = make(
			[]*RosettaTypes.Transaction,
			len(block.Transactions())+1,
		)
		transactions[0] = rewardTx
		index++
	} else {
		transactions = make(
			[]*RosettaTypes.Transaction,
			len(block.Transactions()),
		)
	}

	for _, tx := range loadedTransactions {
		transaction, err := tc.populateTransaction(
			tx,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot parse %s", err, tx.Transaction.Hash().Hex())
		}

		transactions[index] = transaction
		index++
	}

	return transactions, nil
}

// populateRewardTransaction parse rewards to transaction
func (tc *Client) populateRewardTransaction(
	ctx context.Context,
	blockIdentifier *RosettaTypes.BlockIdentifier,
) (*RosettaTypes.Transaction, error) {
	rewards, err := tc.GetBlockReward(ctx, XDPoSChaincommon.HexToHash(blockIdentifier.Hash))
	if err != nil {
		return nil, err
	}
	rewardOperations := []*RosettaTypes.Operation{}
	if rewards != nil {
		for _, signer := range rewards {
			for holder, amount := range signer {
				rewardOperations = append(rewardOperations, &RosettaTypes.Operation{
					OperationIdentifier: &RosettaTypes.OperationIdentifier{
						Index: int64(len(rewardOperations)),
					},
					RelatedOperations: nil,
					Type:              common.MinerRewardOpType,
					Status:            &common.SUCCESS,
					Account: &RosettaTypes.AccountIdentifier{
						Address: MustChecksum(holder),
					},
					Amount: &RosettaTypes.Amount{
						Value:    amount.String(),
						Currency: common.XDCNativeCoin,
					},
				})
			}
		}

	}
	return &RosettaTypes.Transaction{
		TransactionIdentifier: &RosettaTypes.TransactionIdentifier{Hash: blockIdentifier.Hash},
		Operations:            rewardOperations,
	}, nil

}

func (tc *Client) populateTransaction(
	tx *loadedTransaction,
) (*RosettaTypes.Transaction, error) {
	ops := []*RosettaTypes.Operation{}

	// Compute fee operations
	feeOps := feeOps(tx)
	ops = append(ops, feeOps...)

	// Compute trace operations
	traces := flattenTraces(tx.Trace, []*flatCall{})

	traceOps := traceOps(traces, len(ops))
	ops = append(ops, traceOps...)

	// Marshal receipt and trace data
	// TODO: replace with marshalJSONMap (used in `services`)
	receiptBytes, err := tx.Receipt.MarshalJSON()
	if err != nil {
		return nil, err
	}

	var receiptMap map[string]interface{}
	if err := json.Unmarshal(receiptBytes, &receiptMap); err != nil {
		return nil, err
	}

	var traceMap map[string]interface{}
	if err := json.Unmarshal(tx.RawTrace, &traceMap); err != nil {
		return nil, err
	}
	specialReward := new(big.Int)
	addrFrom := (*tx.From).Hex()
	if addr, ok := common.SpecialRewardBlockMap[cast.ToUint64(*tx.BlockNumber)]; ok {
		if strings.ToLower(addr) == strings.ToLower(addrFrom) {
			bal := common.SpecialRewardAddrMap[addr]

			specialReward.SetString(bal+"000000000000000000", 10)
		}
	}
	if specialReward.Sign() > 0 {
		ops = append(ops, &RosettaTypes.Operation{
			OperationIdentifier: &RosettaTypes.OperationIdentifier{
				Index: int64(len(ops)),
			},
			Type:   common.MinerRewardOpType,
			Status: &common.SUCCESS,
			Account: &RosettaTypes.AccountIdentifier{
				Address: MustChecksum(addrFrom),
			},
			Amount: &RosettaTypes.Amount{
				Value:    specialReward.String(),
				Currency: common.XDCNativeCoin,
			},
		})
	}

	populatedTransaction := &RosettaTypes.Transaction{
		TransactionIdentifier: &RosettaTypes.TransactionIdentifier{
			Hash: tx.Transaction.Hash().Hex(),
		},
		Operations: ops,
		Metadata: map[string]interface{}{
			common.METADATA_GAS_LIMIT: hexutil.EncodeUint64(tx.Transaction.Gas()),
			common.METADATA_GAS_PRICE: hexutil.EncodeBig(tx.Transaction.GasPrice()),
			common.METADATA_RECEIPT:   receiptMap,
			common.METADATA_TRACE:     traceMap,
		},
	}

	return populatedTransaction, nil
}

func (tc *Client) EstimateGas(ctx context.Context, msg common.CallArgs) (uint64, error) {
	var result hexutil.Uint64
	err := tc.c.CallContext(ctx, &result, common.RPC_METHOD_ESTIMATE_GAS, msg)
	return uint64(result), err
}

// Balance returns the balance of a *RosettaTypes.AccountIdentifier
// at a *RosettaTypes.PartialBlockIdentifier.
//
// We must use graphql to get the balance atomically (the
// rpc method for balance does not allow for querying
// by block hash nor return the block hash where
// the balance was fetched).
func (tc *Client) Balance(ctx context.Context, account *RosettaTypes.AccountIdentifier, blockIdentifier *RosettaTypes.PartialBlockIdentifier) (res *RosettaTypes.AccountBalanceResponse, err error) {
	block, err := tc.Block(ctx, blockIdentifier)
	if err != nil {
		return nil, err
	}
	res = &RosettaTypes.AccountBalanceResponse{}
	res.BlockIdentifier = block.BlockIdentifier

	var result hexutil.Big
	err = tc.c.CallContext(ctx, &result, common.RPC_METHOD_GET_BALANCE, XDPoSChaincommon.HexToAddress(account.Address), toBlockNumArg(big.NewInt(block.BlockIdentifier.Index)))
	if err != nil {
		return nil, err
	}
	balance := (*big.Int)(&result)

	// TODO: support native coin XDC only, tokens are not available yet
	res.Balances = []*RosettaTypes.Amount{
		{
			Value:    balance.String(),
			Currency: common.XDCNativeCoin,
		},
	}

	// attach nonce
	nonce, err := tc.NonceAt(ctx, XDPoSChaincommon.HexToAddress(account.Address), toBlockNumArg(big.NewInt(block.BlockIdentifier.Index)))
	if err != nil {
		return nil, err
	}
	res.Metadata = map[string]interface{}{
		common.METADATA_ACCOUNT_SEQUENCE: nonce,
	}
	return res, nil
}

func (tc *Client) GetBlockTransactions(ctx context.Context, hash XDPoSChaincommon.Hash) (res []*RosettaTypes.Transaction, err error) {
	hashString := hash.Hex()
	block, err := tc.Block(ctx, &RosettaTypes.PartialBlockIdentifier{
		Hash: &hashString,
	})
	if err != nil {
		return []*RosettaTypes.Transaction{}, err
	}
	return block.Transactions, nil
}

func (tc *Client) SubmitTx(ctx context.Context, signedTx hexutil.Bytes) (string, error) {
	hash := XDPoSChaincommon.Hash{}
	err := tc.c.CallContext(ctx, &hash, common.RPC_METHOD_SEND_SIGNED_TRANSACTION, signedTx)
	if err != nil {
		return "", err
	}

	return hash.String(), nil
}

// GetBlockReward returns rewards of checkpoint block
func (tc *Client) GetBlockReward(ctx context.Context, hash XDPoSChaincommon.Hash) (map[string]map[string]*big.Int, error) {
	rewards := map[string]map[string]map[string]*big.Int{}
	if err := tc.c.CallContext(ctx, &rewards, common.RPC_METHOD_GET_REWARD_BY_HASH, hash); err != nil {
		return nil, err
	}
	if rewards["rewards"] != nil {
		return rewards["rewards"], nil
	}
	return nil, nil
}

// Status returns geth status information
// for determining node healthiness.
func (tc *Client) Status(ctx context.Context) (
	*RosettaTypes.BlockIdentifier,
	int64,
	*RosettaTypes.SyncStatus,
	[]*RosettaTypes.Peer,
	error,
) {
	header, err := tc.blockHeader(ctx, nil)
	if err != nil {
		return nil, -1, nil, nil, err
	}

	progress, err := tc.syncProgress(ctx)
	if err != nil {
		return nil, -1, nil, nil, err
	}

	var syncStatus *RosettaTypes.SyncStatus
	if progress != nil {
		currentIndex := int64(progress.CurrentBlock)
		targetIndex := int64(progress.HighestBlock)

		syncStatus = &RosettaTypes.SyncStatus{
			CurrentIndex: &currentIndex,
			TargetIndex:  &targetIndex,
		}
	}

	peers, err := tc.peers(ctx)
	if err != nil {
		return nil, -1, nil, nil, err
	}

	return &RosettaTypes.BlockIdentifier{
			Hash:  header.Hash().Hex(),
			Index: header.Number.Int64(),
		},
		convertTime(header.Time.Uint64()),
		syncStatus,
		peers,
		nil
}

// syncProgress retrieves the current progress of the sync algorithm. If there's
// no sync currently running, it returns nil.
func (tc *Client) syncProgress(ctx context.Context) (*XDPoSChain.SyncProgress, error) {
	var raw json.RawMessage
	if err := tc.c.CallContext(ctx, &raw, "eth_syncing"); err != nil {
		return nil, err
	}

	var syncing bool
	if err := json.Unmarshal(raw, &syncing); err == nil {
		return nil, nil // Not syncing (always false)
	}

	var progress rpcProgress
	if err := json.Unmarshal(raw, &progress); err != nil {
		return nil, err
	}

	return &XDPoSChain.SyncProgress{
		StartingBlock: uint64(progress.StartingBlock),
		CurrentBlock:  uint64(progress.CurrentBlock),
		HighestBlock:  uint64(progress.HighestBlock),
		PulledStates:  uint64(progress.PulledStates),
		KnownStates:   uint64(progress.KnownStates),
	}, nil
}

// Call handles calls to the /call endpoint.
func (tc *Client) Call(
	ctx context.Context,
	request *RosettaTypes.CallRequest,
) (*RosettaTypes.CallResponse, error) {
	switch request.Method { // nolint:gocritic
	case common.RPC_METHOD_GET_TRANSACTION_RECEIPT:
		var input GetTransactionReceiptInput
		if err := RosettaTypes.UnmarshalMap(request.Parameters, &input); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrCallParametersInvalid, err.Error())
		}

		if len(input.TxHash) == 0 {
			return nil, fmt.Errorf("%w:tx_hash missing from params", ErrCallParametersInvalid)
		}

		receipt, err := tc.transactionReceipt(ctx, XDPoSChaincommon.HexToHash(input.TxHash))
		if err != nil {
			return nil, err
		}

		// We cannot use RosettaTypes.MarshalMap because geth uses a custom
		// marshaler to convert *types.Receipt to JSON.
		jsonOutput, err := receipt.MarshalJSON()
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrCallOutputMarshal, err.Error())
		}

		var receiptMap map[string]interface{}
		if err := json.Unmarshal(jsonOutput, &receiptMap); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrCallOutputMarshal, err.Error())
		}

		// We must encode data over the wire so we can unmarshal correctly
		return &RosettaTypes.CallResponse{
			Result: receiptMap,
		}, nil
	}

	return nil, fmt.Errorf("%w: %s", ErrCallMethodInvalid, request.Method)
}

// derive XDPoSChain Address from uncompressed public key (65 bytes)
// if you have compressed public key in 33 bytes format, please decompress it following this sample code
/**
pubkey, err := crypto.DecompressPubkey(request.PublicKey.Bytes)
if err != nil {
return nil, common.ErrUnableToDecompressPubkey
}
pubBytes := crypto.FromECDSAPub(pubkey)
*/

func PubToAddress(pubkey []byte) XDPoSChaincommon.Address {
	var address XDPoSChaincommon.Address
	copy(address[:], crypto.Keccak256(pubkey[1:])[12:])
	return address
}

func GetCoinbaseFromHeader(header *XDPoSChaintypes.Header) (XDPoSChaincommon.Address, error) {
	if len(header.Extra) < common.ExtraSeal {
		return XDPoSChaincommon.Address{}, fmt.Errorf("extra-data %d byte suffix signature missing", common.ExtraSeal)
	}
	signature := header.Extra[len(header.Extra)-common.ExtraSeal:]
	pubkey, err := crypto.Ecrecover(XDPoS.SigHash(header).Bytes(), signature)
	if err != nil {
		return XDPoSChaincommon.Address{}, err
	}
	return PubToAddress(pubkey), nil
}
