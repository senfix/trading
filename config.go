package trading

import "github.com/senfix/trading/config"

type Config struct {
	Zb      config.Zb      `json:"zb"`
	Binance config.Binance `json:"binance"`
	Bittrex config.Bittrex `json:"bittrex"`
	Okex    config.Okex    `json:"okex"`
}
