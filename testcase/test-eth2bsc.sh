
# in
# swapin
../build/bin/gethscan scanswap --config config-eth.toml --gateway https://nodes.mewapi.io/rpc/eth --start 13548646 --end 13548647
#tx.Hash().Hex(): 0xdad2efd9da22367992c7bc8240e608a086dc208e0cc0e3301ce5a6b4c19f861c, tokenCfg.PairID: ETH2FTM-USDCoin, targetContract: 0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48, from: 0x1B6138ef934CeB32097A30D562c729D9a77BEd82, receiver: 0xC564EE9f21Ed8A2d8E7e76c085740d5e4c5FaFbE, value: 9.9975e+10, chain: eth, type: swapin

# mint
../build/bin/gethscan scanswap --config config-bsc.toml --gateway https://bsc-dataseed1.ninicoin.io/ --start 12353364 --end 12353365
#tx.Hash().Hex(): 0x6ccbc8403abb2aa5079a477a1a5a298053d1a084f1a714a161b53eb4cde52986, tokenCfg.PairID: ETH2FTM-USDCoin, targetContract: 0x04068da6c83afcfa0e13ba15a6696662335d5b75, from: 0xC564EE9f21Ed8A2d8E7e76c085740d5e4c5FaFbE, receiver: 0x1B6138ef934CeB32097A30D562c729D9a77BEd82, value: 9.9975e+10, chain: ftm, type: mint

# ==============================================
# out
# burn
../build/bin/gethscan scanswap --config config-bsc.toml --gateway https://bsc-dataseed1.ninicoin.io/ --start 12352040 --end 12352041
#tx.Hash().Hex(): 0x8625e46d8f708d4db09bf564291c150951b5a853348befdd934b0edc69ac5eeb, tokenCfg.PairID: ETH2FTM-USDCoin, targetContract: 0x04068da6c83afcfa0e13ba15a6696662335d5b75, from: 0xBb6da379Ed680839c4E1Eb7fE49814cD6e7Cbf8a, receiver: 0xBb6da379Ed680839c4E1Eb7fE49814cD6e7Cbf8a, value: 8.2572702e+08, chain: ftm, type: burn

# swapout
../build/bin/gethscan scanswap --config config-eth.toml --gateway https://nodes.mewapi.io/rpc/eth --start 13548369 --end 13548370
#tx.Hash().Hex(): 0x8690669b5f1080aec37acee337566db0169ce06034e0a017486bc10d0b8710ac, tokenCfg.PairID: ETH2FTM-USDCoin, targetContract: 0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48, from: 0xC564EE9f21Ed8A2d8E7e76c085740d5e4c5FaFbE, receiver: 0xBb6da379Ed680839c4E1Eb7fE49814cD6e7Cbf8a, value: 7.8572702e+08, chain: eth, type: swapout
