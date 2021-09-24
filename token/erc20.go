package token

import (
	com "github.com/weijun-sh/balance/common"
)

var erc20CodeParts = map[string][]byte{
        "name":         common.FromHex("0x06fdde03"),
        "symbol":       common.FromHex("0x95d89b41"),
        "decimal":      common.FromHex("0x313ce567"),
        "balanceOf":    common.FromHex("0x70a08231"),
}

// GetErc20Balance get erc20 decimal balacne of address
func GetErc20Decimal(client *ethclient.Client, contract string) (*big.Int, error) {
        data := make([]byte, 4)
        copy(data[:4], erc20CodeParts["decimal"])
        to := common.HexToAddress(contract)
        msg := ethereum.CallMsg{
                To:   &to,
                Data: data,
        }
        result, err := client.CallContract(context.Background(), msg, nil)
        if err != nil {
                return nil, err
        }
        b := fmt.Sprintf("0x%v", hex.EncodeToString(result))
        return com.GetBigIntFromStr(b)
}


