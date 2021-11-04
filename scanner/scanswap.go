package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/anyswap/CrossChain-Bridge/cmd/utils"
	"github.com/anyswap/CrossChain-Bridge/log"
	"github.com/anyswap/CrossChain-Bridge/rpc/client"
	"github.com/anyswap/CrossChain-Bridge/tokens"
	"github.com/urfave/cli/v2"

	//ethclient "github.com/jowenshaw/gethclient"
	"github.com/ethereum/go-ethereum/ethclient"
	com "github.com/jowenshaw/gethclient/common"
	"github.com/ethereum/go-ethereum/common"
	//"github.com/jowenshaw/gethclient/types"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/weijun-sh/gethscan-repo/params"
	"github.com/weijun-sh/gethscan-repo/tools"
	"github.com/weijun-sh/gethscan-repo/mongodb"
	"github.com/weijun-sh/gethscan-repo/token"
)

var (
	scanReceiptFlag = &cli.BoolFlag{
		Name:  "scanReceipt",
		Usage: "scan transaction receipt instead of transaction",
	}

	startHeightFlag = &cli.Int64Flag{
		Name:  "start",
		Usage: "start height (start inclusive)",
		Value: -200,
	}

	timeoutFlag = &cli.Uint64Flag{
		Name:  "timeout",
		Usage: "timeout of scanning one block in seconds",
		Value: 300,
	}

	// ScanSwapCommand scan swaps on eth like blockchain
	ScanSwapCommand = &cli.Command{
		Action:    scanSwap,
		Name:      "scanswap",
		Usage:     "scan cross chain swaps",
		ArgsUsage: " ",
		Description: `
scan cross chain swaps
`,
		Flags: []cli.Flag{
			utils.ConfigFileFlag,
			utils.GatewayFlag,
			scanReceiptFlag,
			startHeightFlag,
			utils.EndHeightFlag,
			utils.StableHeightFlag,
			utils.JobsFlag,
			timeoutFlag,
		},
	}

	transferFuncHash       = common.FromHex("0xa9059cbb")
	transferFromFuncHash   = common.FromHex("0x23b872dd")
	addressSwapoutFuncHash = common.FromHex("0x628d6cba") // for ETH like `address` type address
	stringSwapoutFuncHash  = common.FromHex("0xad54056d") // for BTC like `string` type address

	transferLogTopic       = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")
	addressSwapoutLogTopic = common.HexToHash("0x6b616089d04950dc06c45c6dd787d657980543f89651aec47924752c7d16c888")
	stringSwapoutLogTopic  = common.HexToHash("0x9c92ad817e5474d30a4378deface765150479363a897b0590fbb12ae9d89396b")
	transferLogTopic_2       = common.HexToHash("0x05d0634fe981be85c22e2942a880821b70095d84e152c3ea3c17a4e4250d9d61")
	addressSwapoutLogTopic_2 = common.HexToHash("0x6b616089d04950dc06c45c6dd787d657980543f89651aec47924752c7d16c888")
	stringSwapoutLogTopic_2  = common.HexToHash("")

	routerAnySwapOutTopic                  = common.FromHex("0x97116cf6cd4f6412bb47914d6db18da9e16ab2142f543b86e207c24fbd16b23a")
	routerAnySwapTradeTokensForTokensTopic = common.FromHex("0xfea6abdf4fd32f20966dff7619354cd82cd43dc78a3bee479f04c74dbfc585b3")
	routerAnySwapTradeTokensForNativeTopic = common.FromHex("0x278277e0209c347189add7bd92411973b5f6b8644f7ac62ea1be984ce993f8f4")

	logNFT721SwapOutTopic       = common.FromHex("0x0d45b0b9f5add3e1bb841982f1fa9303628b0b619b000cb1f9f1c3903329a4c7")
	logNFT1155SwapOutTopic      = common.FromHex("0x5058b8684cf36ffd9f66bc623fbc617a44dd65cf2273306d03d3104af0995cb0")
	logNFT1155SwapOutBatchTopic = common.FromHex("0xaa428a5ab688b49b415401782c170d216b33b15711d30cf69482f570eca8db38")
)

const (
	postSwapSuccessResult   = "success"
	bridgeSwapExistKeywords = "mgoError: Item is duplicate"
	routerSwapExistResult   = "alreday registered"
	httpTimeoutKeywords     = "Client.Timeout exceeded while awaiting headers"
	rpcQueryErrKeywords     = "rpc query error"
	errDepositLogNotFountorRemoved = "return error: json-rpc error -32099, verify swap failed! deposit log not found or removed"

	swap_1half = iota
	swap_2half
)

var startHeightArgument int64

var (
	chain         string
	mongodbEnable bool
	chainSwap     chan swapConfig
	Decimal       map[uint64]*big.Float = make(map[uint64]*big.Float)
)

type ethSwapScanner struct {
	gateway     string
	scanReceipt bool

	chainID *big.Int

	endHeight    uint64
	stableHeight uint64
	jobCount     uint64

	processBlockTimeout time.Duration
	processBlockTimers  []*time.Timer

	client *ethclient.Client
	ctx    context.Context

	rpcInterval   time.Duration
	rpcRetryCount int

	cachedSwapPosts *tools.Ring
}

type swapPost struct {
	// common
	txid       string
	rpcMethod  string
	swapServer string

	// bridge
	pairID string

	// router
	chainID  string
	logIndex string
}

func scanSwap(ctx *cli.Context) error {
	utils.SetLogger(ctx)
	params.LoadConfig(utils.GetConfigFilePath(ctx))
	go params.WatchAndReloadScanConfig()

	scanner := &ethSwapScanner{
		ctx:           context.Background(),
		rpcInterval:   1 * time.Second,
		rpcRetryCount: 3,
	}
	scanner.gateway = ctx.String(utils.GatewayFlag.Name)
	scanner.scanReceipt = ctx.Bool(scanReceiptFlag.Name)
	startHeightArgument = ctx.Int64(startHeightFlag.Name)
	scanner.endHeight = ctx.Uint64(utils.EndHeightFlag.Name)
	scanner.stableHeight = ctx.Uint64(utils.StableHeightFlag.Name)
	scanner.jobCount = ctx.Uint64(utils.JobsFlag.Name)
	scanner.processBlockTimeout = time.Duration(ctx.Uint64(timeoutFlag.Name)) * time.Second

	log.Info("get argument success",
		"gateway", scanner.gateway,
		"scanReceipt", scanner.scanReceipt,
		"start", startHeightArgument,
		"end", scanner.endHeight,
		"stable", scanner.stableHeight,
		"jobs", scanner.jobCount,
		"timeout", scanner.processBlockTimeout,
	)

       //mongo
       dbConfig := params.GetMongodbConfig()
       mongodbEnable = dbConfig.Enable
       if mongodbEnable {
               InitMongodb()
               go scanner.loopSwapPending()
       }

       chConfig := params.GetChainConfig()
       chain = chConfig.BlockChain

	scanner.initClient()
	//scanner.initDecimal()
	scanner.run()
	return nil
}

func (scanner *ethSwapScanner) initDecimal() {
	c := params.GetScanConfig()
	for _, tokenCfg := range c.Tokens {
		decimal, _ := token.GetErc20Decimal(scanner.client, tokenCfg.TokenAddress)
		fmt.Printf("tolen: %v decimal: %v\n", tokenCfg.TokenAddress, decimal)
	}
}

func (scanner *ethSwapScanner) initClient() {
	ethcli, err := ethclient.Dial(scanner.gateway)
	if err != nil {
		log.Fatal("ethclient.Dail failed", "gateway", scanner.gateway, "err", err)
	}
	log.Info("ethclient.Dail gateway success", "gateway", scanner.gateway)
	scanner.client = ethcli
	scanner.chainID, err = ethcli.ChainID(scanner.ctx)
	if err != nil {
		log.Fatal("get chainID failed", "err", err)
	}
	log.Info("get chainID success", "chainID", scanner.chainID)
}

func (scanner *ethSwapScanner) run() {
	scanner.cachedSwapPosts = tools.NewRing(100)
	go scanner.repostCachedSwaps()

	scanner.processBlockTimers = make([]*time.Timer, scanner.jobCount+1)
	for i := 0; i < len(scanner.processBlockTimers); i++ {
		scanner.processBlockTimers[i] = time.NewTimer(scanner.processBlockTimeout)
	}

	wend := scanner.endHeight
	if wend == 0 {
		wend = scanner.loopGetLatestBlockNumber()
	}
	if startHeightArgument != 0 {
		var start uint64
		if startHeightArgument > 0 {
			start = uint64(startHeightArgument)
		} else if startHeightArgument < 0 {
			start = wend - uint64(-startHeightArgument)
		}
		scanner.doScanRangeJob(start, wend)
	}
	if scanner.endHeight == 0 {
		scanner.scanLoop(wend)
	}
}

func (scanner *ethSwapScanner) doScanRangeJob(start, end uint64) {
	log.Info("start scan range job", "start", start, "end", end, "jobs", scanner.jobCount)
	if scanner.jobCount == 0 {
		log.Fatal("zero count jobs specified")
	}
	if start >= end {
		log.Fatalf("wrong scan range [%v, %v)", start, end)
	}
	jobs := scanner.jobCount
	count := end - start
	step := count / jobs
	if step == 0 {
		jobs = 1
		step = count
	}
	wg := new(sync.WaitGroup)
	for i := uint64(0); i < jobs; i++ {
		from := start + i*step
		to := start + (i+1)*step
		if i+1 == jobs {
			to = end
		}
		wg.Add(1)
		go scanner.scanRange(i+1, from, to, wg)
	}
	if scanner.endHeight != 0 {
		wg.Wait()
	}
}

func (scanner *ethSwapScanner) scanRange(job, from, to uint64, wg *sync.WaitGroup) {
	defer wg.Done()
	log.Info(fmt.Sprintf("[%v] scan range", job), "from", from, "to", to)

	for h := from; h < to; h++ {
		scanner.scanBlock(job, h, false)
	}

	log.Info(fmt.Sprintf("[%v] scan range finish", job), "from", from, "to", to)
}

func (scanner *ethSwapScanner) scanLoop(from uint64) {
	stable := scanner.stableHeight
	log.Info("start scan loop job", "from", from, "stable", stable)
	for {
		latest := scanner.loopGetLatestBlockNumber()
		for h := from; h <= latest; h++ {
			scanner.scanBlock(0, h, true)
		}
		if from+stable < latest {
			from = latest - stable
		}
		time.Sleep(1 * time.Second)
	}
}

func (scanner *ethSwapScanner) loopGetLatestBlockNumber() uint64 {
	for { // retry until success
		header, err := scanner.client.HeaderByNumber(scanner.ctx, nil)
		if err == nil {
			log.Info("get latest block number success", "height", header.Number)
			return header.Number.Uint64()
		}
		log.Warn("get latest block number failed", "err", err)
		time.Sleep(scanner.rpcInterval)
	}
}

func (scanner *ethSwapScanner) loopGetTxReceipt(txHash common.Hash) (receipt *types.Receipt, err error) {
	for i := 0; i < 5; i++ { // with retry
		receipt, err = scanner.client.TransactionReceipt(scanner.ctx, txHash)
		if err == nil {
			if receipt.Status != 1 {
				log.Debug("tx with wrong receipt status", "txHash", txHash.Hex())
				return nil, errors.New("tx with wrong receipt status")
			}
			return receipt, nil
		}
		time.Sleep(scanner.rpcInterval)
	}
	return nil, err
}

func (scanner *ethSwapScanner) loopGetBlock(height uint64) (block *types.Block, err error) {
	blockNumber := new(big.Int).SetUint64(height)
	for i := 0; i < 5; i++ { // with retry
		block, err = scanner.client.BlockByNumber(scanner.ctx, blockNumber)
		if err == nil {
			return block, nil
		}
		log.Warn("get block failed", "height", height, "err", err)
		time.Sleep(scanner.rpcInterval)
	}
	return nil, err
}

func (scanner *ethSwapScanner) scanBlock(job, height uint64, cache bool) {
	block, err := scanner.loopGetBlock(height)
	if err != nil {
		return
	}
	blockHash := block.Hash().Hex()
	if cache && cachedBlocks.isScanned(blockHash) {
		return
	}
	log.Info(fmt.Sprintf("[%v] scan block %v", job, height), "hash", blockHash, "txs", len(block.Transactions()))

	scanner.processBlockTimers[job].Reset(scanner.processBlockTimeout)
SCANTXS:
	for i, tx := range block.Transactions() {
		select {
		case <-scanner.processBlockTimers[job].C:
			log.Warn(fmt.Sprintf("[%v] scan block %v timeout", job, height), "hash", blockHash, "txs", len(block.Transactions()))
			break SCANTXS
		default:
			log.Debug(fmt.Sprintf("[%v] scan tx in block %v index %v", job, height, i), "tx", tx.Hash().Hex())
			scanner.scanTransaction(tx, block.Time())
		}
	}
	if cache {
		cachedBlocks.addBlock(blockHash)
	}
}

func (scanner *ethSwapScanner) scanTransaction(tx *types.Transaction, ts uint64) {
	if tx.To() == nil {
		return
	}

	txHash := tx.Hash().Hex()

	for _, tokenCfg := range params.GetScanConfig().Tokens {
		verifyErr := scanner.verifyTransaction(tx, tokenCfg, ts)
		if verifyErr != nil {
			log.Debug("verify tx failed", "txHash", txHash, "err", verifyErr)
		}
	}
}

func (scanner *ethSwapScanner) checkTxToAddress(tx *types.Transaction, tokenCfg *params.TokenConfig) (receipt *types.Receipt, isAcceptToAddr bool) {
	needReceipt := scanner.scanReceipt
	txtoAddress := tx.To().String()

	var cmpTxTo string
	if tokenCfg.IsRouterSwap() {
		cmpTxTo = tokenCfg.RouterContract
		needReceipt = true
	} else if tokenCfg.IsNativeToken() {
		cmpTxTo = tokenCfg.DepositAddress
	} else if tokenCfg.IsBridgeSwap_2() {
		cmpTxTo = tokenCfg.TokenAddress
		needReceipt = true
	} else {
		cmpTxTo = tokenCfg.TokenAddress
		if tokenCfg.CallByContract != "" {
			cmpTxTo = tokenCfg.CallByContract
			needReceipt = true
		}
	}

	if strings.EqualFold(txtoAddress, cmpTxTo) {
		isAcceptToAddr = true
	} else if !tokenCfg.IsNativeToken() {
		for _, whiteAddr := range tokenCfg.Whitelist {
			if strings.EqualFold(txtoAddress, whiteAddr) {
				isAcceptToAddr = true
				needReceipt = true
				break
			}
		}
	}

	if !isAcceptToAddr {
		return nil, false
	}

	if needReceipt {
		r, err := scanner.loopGetTxReceipt(tx.Hash())
		if err != nil {
			log.Warn("get tx receipt error", "txHash", tx.Hash().Hex(), "err", err)
			return nil, false
		}
		receipt = r
	}

	return receipt, true
}

// IsNativeToken
func (scanner *ethSwapScanner) checkTxFromAddress(tx *types.Transaction, tokenCfg *params.TokenConfig) (receipt *types.Receipt, isAcceptFromAddr bool) {
	fromAddress, _ := types.Sender(types.LatestSignerForChainID(scanner.chainID), tx)
	txfromAddress := fromAddress.String()

	var cmpTxFrom string
	cmpTxFrom = tokenCfg.DepositAddress

	if strings.EqualFold(txfromAddress, cmpTxFrom) {
		isAcceptFromAddr = true
	}

	if !isAcceptFromAddr {
		return nil, false
	}

	return receipt, true
}

func (scanner *ethSwapScanner) verifyTransaction(tx *types.Transaction, tokenCfg *params.TokenConfig, ts uint64) (verifyErr error) {
	var receipt *types.Receipt
	var isAcceptToAddr bool
	if !tokenCfg.IsBridgeSwapin() && tokenCfg.IsNativeToken() {
		receipt, isAcceptToAddr = scanner.checkTxFromAddress(tx, tokenCfg)
	} else {
		receipt, isAcceptToAddr = scanner.checkTxToAddress(tx, tokenCfg)
	}
	if !isAcceptToAddr {
		return nil
	}

	txHash := tx.Hash().Hex()

	switch {
	// router swap
	case tokenCfg.IsRouterSwap():
		scanner.verifyAndPostRouterSwapTx(tx, receipt, tokenCfg)
		return nil

	// bridge swapin
	case tokenCfg.IsBridgeSwapin():
		if tokenCfg.IsNativeToken() {
			scanner.printNativeInfo(tx, tokenCfg, ts, "swapin")
			return nil
			scanner.postBridgeSwap(txHash, tokenCfg)
			return nil
		}

		verifyErr = scanner.verifyErc20SwapinTx(tx, receipt, tokenCfg, ts)
		// swapin my have multiple deposit addresses for different bridges
		if errors.Is(verifyErr, tokens.ErrTxWithWrongReceiver) {
			return nil
		}

	// bridge swapout
	default:
		if tokenCfg.IsNativeToken() {
			scanner.printNativeInfo(tx, tokenCfg, ts, "swapout")
			return nil
		}
		if scanner.scanReceipt {
			verifyErr = scanner.parseSwapoutTxLogs(tx, receipt.Logs, tokenCfg, ts)
		} else {
			verifyErr = scanner.verifySwapoutTx(tx, receipt, tokenCfg, ts)
		}
	}

	if verifyErr == nil {
		return nil//TODO
		scanner.postBridgeSwap(txHash, tokenCfg)
	}
	return verifyErr
}

func (scanner *ethSwapScanner) printNativeInfo(tx *types.Transaction, tokenCfg *params.TokenConfig, ts uint64, swapType string) {
	receiver := tx.To().String()
	targetContract := tokenCfg.TokenAddress
	fromAddress, _ := types.Sender(types.LatestSignerForChainID(scanner.chainID), tx)
	from := fromAddress.String()
	value := tx.Value()
	valueFloat := getBigFloat4BigInt(value, tokenCfg.Decimal)
	fmt.Printf("tx.Hash().Hex(): %v, tokenCfg.PairID: %v, targetContract: %v, from: %v, receiver: %v, value: %v, chain: %v, type: %v, timestamp: %v\n", tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, valueFloat, chain, swapType, ts)
}

func (scanner *ethSwapScanner) postBridgeSwap(txid string, tokenCfg *params.TokenConfig) {
	pairID := tokenCfg.PairID
	var subject, rpcMethod string
	if tokenCfg.DepositAddress != "" {
		subject = "post bridge swapin register"
		rpcMethod = "swap.Swapin"
	} else {
		subject = "post bridge swapout register"
		rpcMethod = "swap.Swapout"
	}
	log.Info(subject, "txid", txid, "pairID", pairID)
	swap := &swapPost{
		txid:       txid,
		pairID:     pairID,
		rpcMethod:  rpcMethod,
	}
	scanner.postSwapPost(swap)
}

func (scanner *ethSwapScanner) postRouterSwap(txid string, logIndex int, tokenCfg *params.TokenConfig) {
	chainID := tokenCfg.ChainID

	subject := "post router swap register"
	rpcMethod := "swap.RegisterRouterSwap"
	log.Info(subject, "swaptype", tokenCfg.TxType, "chainid", chainID, "txid", txid, "logindex", logIndex)

	swap := &swapPost{
		txid:       txid,
		chainID:    chainID,
		logIndex:   fmt.Sprintf("%d", logIndex),
		rpcMethod:  rpcMethod,
	}
	scanner.postSwapPost(swap)
}

func (scanner *ethSwapScanner) postSwapPost(swap *swapPost) {
	var needCached bool
	var needPending bool = true
	for i := 0; i < scanner.rpcRetryCount; i++ {
		err := rpcPost(swap)
		if err == nil {
			needCached = false
			needPending = false
			break
		}
		if errors.Is(err, tokens.ErrTxNotFound) ||
			strings.Contains(err.Error(), httpTimeoutKeywords) {
			needCached = true
			needPending = true
		}
		time.Sleep(scanner.rpcInterval)
	}
	if needCached {
		log.Warn("cache swap", "swap", swap)
		scanner.cachedSwapPosts.Add(swap)
	}
       if needPending {
               if mongodbEnable {
                       //insert mongo post pending
                       addMongodbSwapPendingPost(swap)
               }
       }
       if !needCached && !needPending {
               if mongodbEnable {
                       //insert mongo post
                       addMongodbSwapPost(swap)
               }
       }
}

func addMongodbSwapPost(swap *swapPost) {
       ms := &mongodb.MgoSwap{
               Id:         swap.txid,
               Txid:       swap.txid,
               PairID:     swap.pairID,
               RpcMethod:  swap.rpcMethod,
               Chain:      chain,
               Timestamp:  uint64(time.Now().Unix()),
       }
       mongodb.AddSwap(ms, false)
}

func addMongodbSwapPendingPost(swap *swapPost) {
       ms := &mongodb.MgoSwap{
               Id:         swap.txid,
               Txid:       swap.txid,
               PairID:     swap.pairID,
               RpcMethod:  swap.rpcMethod,
               Chain:      chain,
               Timestamp:  uint64(time.Now().Unix()),
       }
       mongodb.AddSwapPending(ms, false)
 }

func (scanner *ethSwapScanner) repostCachedSwaps() {
	for {
		scanner.cachedSwapPosts.Do(func(p interface{}) bool {
			return scanner.repostSwap(p.(*swapPost))
		})
		time.Sleep(10 * time.Second)
	}
}

func rpcPost(swap *swapPost) error {
	var isRouterSwap bool
	var args interface{}
	if swap.pairID != "" {
		args = map[string]interface{}{
			"txid":   swap.txid,
			"pairid": swap.pairID,
		}
	} else if swap.logIndex != "" {
		isRouterSwap = true
		args = map[string]string{
			"chainid":  swap.chainID,
			"txid":     swap.txid,
			"logindex": swap.logIndex,
		}
	} else {
		return fmt.Errorf("wrong swap post item %v", swap)
	}

	timeout := 300
	reqID := 666
	var result interface{}
	err := client.RPCPostWithTimeoutAndID(&result, timeout, reqID, swap.swapServer, swap.rpcMethod, args)

	if err != nil {
		if isRouterSwap {
			log.Warn("post router swap failed", "swap", args, "server", swap.swapServer, "err", err)
			return err
		}
		if strings.Contains(err.Error(), bridgeSwapExistKeywords) {
			err = nil // ignore this kind of error
			log.Info("post bridge swap already exist", "swap", args)
		} else {
			log.Warn("post bridge swap failed", "swap", args, "server", swap.swapServer, "err", err)
		}
		return err
	}

	if !isRouterSwap {
		log.Info("post bridge swap success", "swap", args)
		return nil
	}

	var status string
	if res, ok := result.(map[string]interface{}); ok {
		status, _ = res[swap.logIndex].(string)
	}
	if status == "" {
		err = errors.New("post router swap unmarshal result failed")
		log.Error(err.Error(), "swap", args, "server", swap.swapServer, "result", result)
		return err
	}
	switch status {
	case postSwapSuccessResult:
		log.Info("post router swap success", "swap", args)
	case routerSwapExistResult:
		log.Info("post router swap already exist", "swap", args)
	default:
		err = errors.New(status)
		log.Info("post router swap failed", "swap", args, "server", swap.swapServer, "err", err)
	}
	return err
}

func (scanner *ethSwapScanner) repostSwap(swap *swapPost) bool {
	for i := 0; i < scanner.rpcRetryCount; i++ {
		err := rpcPost(swap)
		if err == nil {
			return true
		}
		switch {
		case strings.Contains(err.Error(), rpcQueryErrKeywords):
		case strings.Contains(err.Error(), httpTimeoutKeywords):
		default:
			return false
		}
		time.Sleep(scanner.rpcInterval)
	}
	return false
}

func (scanner *ethSwapScanner) getSwapoutFuncHashByTxType(txType string) []byte {
	switch strings.ToLower(txType) {
	case params.TxSwapout:
		return addressSwapoutFuncHash
	case params.TxSwapout_2:
		return transferFuncHash
	case params.TxSwapout2:
		return stringSwapoutFuncHash
	default:
		log.Errorf("unknown swapout tx type %v", txType)
		return nil
	}
}

func (scanner *ethSwapScanner) getLogTopicByTxType(txType string) (topTopic common.Hash, topicsLen, bridge2 int) {
	switch strings.ToLower(txType) {
	case params.TxSwapin:
		return transferLogTopic, 3, swap_1half
	case params.TxSwapout:
		return addressSwapoutLogTopic, 3, swap_1half
	case params.TxSwapout2:
		return stringSwapoutLogTopic, 2, swap_1half
	case params.TxSwapin_2:
		return transferLogTopic_2, 3, swap_2half
	case params.TxSwapout_2:
		return addressSwapoutLogTopic_2, 3, swap_2half
	case params.TxSwapout2_2:
		return stringSwapoutLogTopic_2, 2, swap_2half
	default:
		log.Errorf("unknown tx type %v", txType)
		return common.Hash{}, 0, 0
	}
}

func (scanner *ethSwapScanner) verifyErc20SwapinTx(tx *types.Transaction, receipt *types.Receipt, tokenCfg *params.TokenConfig, ts uint64) (err error) {
	if receipt == nil {
		err = scanner.parseErc20SwapinTxInput(tx, tokenCfg, ts)
	} else {
		err = scanner.parseErc20SwapinTxLogs(tx, receipt.Logs, tokenCfg, ts)
	}
	return err
}

func (scanner *ethSwapScanner) verifySwapoutTx(tx *types.Transaction, receipt *types.Receipt, tokenCfg *params.TokenConfig, ts uint64) (err error) {
	if receipt == nil {
		err = scanner.parseSwapoutTxInput(tx, tokenCfg, ts)
	} else {
		err = scanner.parseSwapoutTxLogs(tx, receipt.Logs, tokenCfg, ts)
	}
	return err
}

func (scanner *ethSwapScanner) verifyAndPostRouterSwapTx(tx *types.Transaction, receipt *types.Receipt, tokenCfg *params.TokenConfig) {
	if receipt == nil {
		return
	}
	for i := 1; i < len(receipt.Logs); i++ {
		rlog := receipt.Logs[i]
		if rlog.Removed {
			continue
		}
		if !strings.EqualFold(rlog.Address.String(), tokenCfg.RouterContract) {
			continue
		}
		logTopic := rlog.Topics[0].Bytes()
		switch {
		case tokenCfg.IsRouterERC20Swap():
			switch {
			case bytes.Equal(logTopic, routerAnySwapOutTopic):
			case bytes.Equal(logTopic, routerAnySwapTradeTokensForTokensTopic):
			case bytes.Equal(logTopic, routerAnySwapTradeTokensForNativeTopic):
			default:
				continue
			}
		case tokenCfg.IsRouterNFTSwap():
			switch {
			case bytes.Equal(logTopic, logNFT721SwapOutTopic):
			case bytes.Equal(logTopic, logNFT1155SwapOutTopic):
			case bytes.Equal(logTopic, logNFT1155SwapOutBatchTopic):
			default:
				continue
			}
		}
		scanner.postRouterSwap(tx.Hash().Hex(), i, tokenCfg)
	}
}

func (scanner *ethSwapScanner) parseErc20SwapinTxInput(tx *types.Transaction, tokenCfg *params.TokenConfig, ts uint64) error {
	input := tx.Data()
	depositAddress := tokenCfg.DepositAddress
	if len(input) < 4 {
		return tokens.ErrTxWithWrongInput
	}
	var receiver string
	value := new(big.Int).SetUint64(0)
	funcHash := input[:4]
	switch {
	case bytes.Equal(funcHash, transferFuncHash):
		receiver = common.BytesToAddress(com.GetData(input, 4, 32)).Hex()
		value = new(big.Int).SetBytes(com.GetData(input, 36, 32))
	case bytes.Equal(funcHash, transferFromFuncHash):
		receiver = common.BytesToAddress(com.GetData(input, 36, 32)).Hex()
		value = new(big.Int).SetBytes(com.GetData(input, 68, 32))
	default:
		return tokens.ErrTxFuncHashMismatch
	}
	if !strings.EqualFold(receiver, depositAddress) {
		return tokens.ErrTxWithWrongReceiver
	}

	targetContract := tokenCfg.TokenAddress
	fromAddress, _ := types.Sender(types.LatestSignerForChainID(scanner.chainID), tx)
	from := fromAddress.String()
	valueFloat := getBigFloat4BigInt(value, tokenCfg.Decimal)
	fmt.Printf("tx.Hash().Hex(): %v, tokenCfg.PairID: %v, targetContract: %v, from: %v, receiver: %v, value: %v, chain: %v, type: %v, timestamp: %v\n", tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, valueFloat, chain, "swapin", ts)
	//swapStruct := mergeStruct(tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, value, chain, "swapin")
	//chainSwap <- swapStruct
	return nil
}

func getBigFloat4BigInt(value *big.Int, decimal uint64) *big.Float {
	valueFloatTmp := new(big.Float).SetInt(value)
	if Decimal[decimal] == nil {
		Decimal[decimal] = big.NewFloat(math.Pow(10, float64(decimal)))
	}
	valueFloat := new(big.Float).Quo(valueFloatTmp, Decimal[decimal])
	return valueFloat
}

func (scanner *ethSwapScanner) parseErc20SwapinTxLogs(tx *types.Transaction, logs []*types.Log, tokenCfg *params.TokenConfig, ts uint64) (err error) {
	chainSwap = make(chan swapConfig, 100)
	targetContract := tokenCfg.TokenAddress
	depositAddress := tokenCfg.DepositAddress
	cmpLogTopic, topicsLen, half := scanner.getLogTopicByTxType(tokenCfg.TxType)
	fromAddress, _ := types.Sender(types.LatestSignerForChainID(scanner.chainID), tx)
	from := fromAddress.String()

	transferLogExist := false
	for _, rlog := range logs {
		if rlog.Removed {
			continue
		}
		if !strings.EqualFold(rlog.Address.Hex(), targetContract) {
			continue
		}
		if len(rlog.Topics) != topicsLen || rlog.Data == nil {
			continue
		}
		if rlog.Topics[0] != cmpLogTopic {
			continue
		}
		transferLogExist = true
		receiver := common.BytesToAddress(rlog.Topics[2][:]).Hex()
		value := new(big.Int).SetBytes(rlog.Data)
		if half == swap_2half {// second half
			if strings.EqualFold(from, depositAddress) {
				valueFloat := getBigFloat4BigInt(value, tokenCfg.Decimal)
				fmt.Printf("tx.Hash().Hex(): %v, tokenCfg.PairID: %v, targetContract: %v, from: %v, receiver: %v, value: %v, chain: %v, type: %v, timestamp: %v\n", tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, valueFloat, chain, "mint", ts)
				//swapStruct := mergeStruct(tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, value, chain, "mint")
				//chainSwap <- swapStruct
				return nil
			}
		} else {
			if strings.EqualFold(receiver, depositAddress) {
				valueFloat := getBigFloat4BigInt(value, tokenCfg.Decimal)
				fmt.Printf("tx.Hash().Hex(): %v, tokenCfg.PairID: %v, targetContract: %v, from: %v, receiver: %v, value: %v, chain: %v, type: %v, timestamp: %v\n", tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, valueFloat, chain, "swapin", ts)
				//swapStruct := mergeStruct(tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, value, chain, "swapin")
				//chainSwap <- swapStruct
				return nil
			}
		}
	}
	if transferLogExist {
		return tokens.ErrTxWithWrongReceiver
	}
	return tokens.ErrDepositLogNotFound
}

type swapConfig struct {
	Id         string `bson:"_id"` //txid
	Txid       string `bson:"txid"`
	PairID     string `bson:"pairID"` //"ETH2BSC-USDC"
	Token  string `bson:"token"`
	From string `bson:"from"`
	To string `bson:"to"`
	Value string `bson:"value"`
	Chain string `bson:"chain"`
	Type string `bson:"type"`
}

func mergeStruct(txHash, pairID, contract, from, to, value, chain, swaptype string) swapConfig {
	return swapConfig{
			Id: txHash,
			Txid: txHash,
			PairID: pairID,
			Token: contract,
			From: from,
			To: to,
			Value: value,
			Chain: chain,
			Type: swaptype,
		}
}

func (scanner *ethSwapScanner) parseSwapoutTxInput(tx *types.Transaction, tokenCfg *params.TokenConfig, ts uint64) error {
	//txType := tokenCfg.TxType
	input := tx.Data()
	if len(input) < 4 {
		return tokens.ErrTxWithWrongInput
	}
	//funcHash := input[:4]
	//if bytes.Equal(funcHash, scanner.getSwapoutFuncHashByTxType(txType)) {
	//	return nil
	//}
	//return tokens.ErrTxFuncHashMismatch
	depositAddress := tokenCfg.DepositAddress
	fromAddress, _ := types.Sender(types.LatestSignerForChainID(scanner.chainID), tx)
	from := fromAddress.String()

	var receiver string
	value := new(big.Int).SetUint64(0)
	funcHash := input[:4]
	switch {
	case bytes.Equal(funcHash, transferFuncHash):
		receiver = common.BytesToAddress(com.GetData(input, 4, 32)).Hex()
		value = new(big.Int).SetBytes(com.GetData(input, 36, 32))
	case bytes.Equal(funcHash, transferFromFuncHash):
		receiver = common.BytesToAddress(com.GetData(input, 36, 32)).Hex()
		value = new(big.Int).SetBytes(com.GetData(input, 68, 32))
	default:
		return tokens.ErrTxFuncHashMismatch
	}
	if !strings.EqualFold(from, depositAddress) {
		return tokens.ErrTxWithWrongReceiver
	}

	targetContract := tokenCfg.TokenAddress
	valueFloat := getBigFloat4BigInt(value, tokenCfg.Decimal)
	fmt.Printf("tx.Hash().Hex(): %v, tokenCfg.PairID: %v, targetContract: %v, from: %v, receiver: %v, value: %v, chain: %v, type: %v, timestamp: %v\n", tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, valueFloat, chain, "swapout", ts)
	//swapStruct := mergeStruct(tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, value, chain, "swapin")
	//chainSwap <- swapStruct
	return nil
}

func (scanner *ethSwapScanner) parseSwapoutTxLogs(tx *types.Transaction, logs []*types.Log, tokenCfg *params.TokenConfig, ts uint64) (err error) {
	targetContract := tokenCfg.TokenAddress
	cmpLogTopic, topicsLen, half := scanner.getLogTopicByTxType(tokenCfg.TxType)
	fromAddress, _ := types.Sender(types.LatestSignerForChainID(scanner.chainID), tx)
	from := fromAddress.String()

	for _, rlog := range logs {
		if rlog.Removed {
			continue
		}
		if !strings.EqualFold(rlog.Address.Hex(), targetContract) {
			continue
		}
		if len(rlog.Topics) != topicsLen || rlog.Data == nil {
			continue
		}
		if rlog.Topics[0] == cmpLogTopic {
			value := new(big.Int).SetBytes(rlog.Data)
			receiver := common.BytesToAddress(rlog.Topics[2][:]).Hex()
			if half == swap_2half {// second half
			//	if strings.EqualFold(from, depositAddress) {
					valueFloat := getBigFloat4BigInt(value, tokenCfg.Decimal)
					fmt.Printf("tx.Hash().Hex(): %v, tokenCfg.PairID: %v, targetContract: %v, from: %v, receiver: %v, value: %v, chain: %v, type: %v, timestamp: %v\n", tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, valueFloat, chain, "burn", ts)
					//swapStruct := mergeStruct(tx.Hash().Hex(), tokenCfg.PairID, targetContract, from, receiver, value, chain, "mint")
					//chainSwap <- swapStruct
					return nil
			//	}
			}
			//return nil
		}
	}
	return tokens.ErrSwapoutLogNotFound
}

type cachedSacnnedBlocks struct {
	capacity  int
	nextIndex int
	hashes    []string
}

var cachedBlocks = &cachedSacnnedBlocks{
	capacity:  100,
	nextIndex: 0,
	hashes:    make([]string, 100),
}

func (cache *cachedSacnnedBlocks) addBlock(blockHash string) {
	cache.hashes[cache.nextIndex] = blockHash
	cache.nextIndex = (cache.nextIndex + 1) % cache.capacity
}

func (cache *cachedSacnnedBlocks) isScanned(blockHash string) bool {
	for _, b := range cache.hashes {
		if b == blockHash {
			return true
		}
	}
	return false
}

// InitMongodb init mongodb by config
func InitMongodb() {
       log.Info("InitMongodb")
       dbConfig := params.GetMongodbConfig()
       mongodb.MongoServerInit([]string{dbConfig.DBURL}, dbConfig.DBName, dbConfig.UserName, dbConfig.Password)
}

func (scanner *ethSwapScanner) loopSwapPending() {
       log.Info("start SwapPending loop job")
       for {
               sp := mongodb.FindAllSwapPending(chain)
               if len(sp) == 0 {
                       time.Sleep(30 * time.Second)
                       continue
               }
               log.Info("loopSwapPending", "swap", sp, "len", len(sp))
               for i, swap := range sp {
                       log.Info("loopSwapPending", "swap", swap, "index", i)
                       sp := swapPost{}
                       sp.txid = swap.Txid
                       sp.pairID = swap.PairID
                       sp.rpcMethod = swap.RpcMethod
                       ok := scanner.repostSwap(&sp)
                       if ok == true {
                               mongodb.UpdateSwapPending(swap)
                       } else {
                               r, err := scanner.loopGetTxReceipt(common.HexToHash(swap.Txid))
                               if err != nil || (err == nil && r.Status != uint64(0)) {
                                       log.Warn("loopSwapPending remove", "status", 0, "txHash", swap.Txid)
                                       mongodb.RemoveSwapPending(swap)
                                       mongodb.AddSwapDeleted(swap, false)
                               }
                       }
               }
               time.Sleep(10 * time.Second)
       }
}
