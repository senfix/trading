package trading

import "github.com/senfix/trader/config"

type Config struct {
	Zb config.Zb `json:"zb"`
	Binance config.Binance `json:"binance"`
	Bittrex config.Bittrex `json:"bittrex"`
}

type Binance struct {
	ApiKey    string `json:"api_key"`
	ApiSecret string `json:"api_secret"`
}

type Bittrex struct {
	ApiKey    string `json:"api_key"`
	ApiSecret string `json:"api_secret"`
}

type Zb struct {
	ApiKey    string `json:"api_key"`
	ApiSecret string `json:"api_secret"`
}

