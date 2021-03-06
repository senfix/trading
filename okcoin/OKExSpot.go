package okcoin

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/okcoin-okex/open-api-v3-sdk/okex-go-sdk-api"
	. "github.com/senfix/trading"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	/*
	  http headers
	*/
	OK_ACCESS_KEY        = "OK-ACCESS-KEY"
	OK_ACCESS_SIGN       = "OK-ACCESS-SIGN"
	OK_ACCESS_TIMESTAMP  = "OK-ACCESS-TIMESTAMP"
	OK_ACCESS_PASSPHRASE = "OK-ACCESS-PASSPHRASE"
	CONTENT_TYPE         = "Content-Type"
	ACCEPT               = "Accept"

	APPLICATION_JSON      = "application/json"
	APPLICATION_JSON_UTF8 = "application/json; charset=UTF-8"
)

type OKExSpot struct {
	OKCoinCN_API
	ws                *WsConn
	createWsLock      sync.Mutex
	wsTickerHandleMap map[string]func(*Ticker)
	wsDepthHandleMap  map[string]func(*Depth)
	wsTradeHandleMap  map[string]func(*Trade)
	oc                *okex.Client
}

func NewOKExSpot(client *http.Client, accesskey, secretkey string) *OKExSpot {
	return &OKExSpot{
		OKCoinCN_API:      OKCoinCN_API{client, accesskey, secretkey, "https://www.okex.com/api/v1/"},
		wsTickerHandleMap: make(map[string]func(*Ticker)),
		wsDepthHandleMap:  make(map[string]func(*Depth)),
		wsTradeHandleMap:  make(map[string]func(*Trade)),
		oc:                NewOKExClient(),
	}
}

func NewOKExClient() *okex.Client {

	var config okex.Config
	config.Endpoint = "https://www.okex.com/"
	config.ApiKey = "a92e50c8-33d1-4a37-8f80-32ed975cf956"
	config.SecretKey = "9A817E077FA102111F9D42331A79AA26"
	config.Passphrase = "3tk0tkYPmSi4"
	config.TimeoutSecond = 45
	config.IsPrint = true
	config.I18n = okex.ENGLISH

	client := okex.NewClient(config)
	return client
}

func (ctx *OKExSpot) GetExchangeName() string {
	return OKEX
}

func (ctx *OKExSpot) Withdraw(wallet Wallet, amount string, currency Currency) (err error) {
	a, err := strconv.ParseFloat(amount, 32)

	if err != nil {
		return err
	}

	//load fee
	//c := currency.String()
	fee := 0.0
	//feeMap, err := ctx.oc.GetAccountWithdrawalFeeByCurrency(&c)
	//if err != nil {
	//	return
	//}
	//for _, data := range *feeMap {
	//	feeStr := data["min_fee"]
	//	fee = feeStr.(float64)
	//}
	fee = 0.00100000

	//transfer to general wallet
	_, err = ctx.oc.PostAccountTransfer(
		strings.ToLower(currency.String()),
		1,
		6,
		float32(a+fee),
		nil,
	)
	if err != nil {
		return
	}

	_, err = ctx.oc.PostAccountWithdrawal(
		strings.ToLower(currency.String()),
		wallet.Address,
		"3tk0tkYPmSi4",
		4,
		float32(a),
		float32(fee),
	)

	return
}

func (ctx *OKExSpot) GetWallet() (w wallet, err error) {
	postData := url.Values{}
	err = ctx.buildPostForm(&postData)
	if err != nil {
		return
	}
	body, err := HttpPostForm(ctx.client, "https://www.okex.com/api/v1/wallet_info.do", postData)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	w = wallet{}

	err = json.Unmarshal(body, &w)
	if err != nil {
		log.Printf("%v", err)
		return
	}

	return
}

func (ctx *OKExSpot) Transfer() (err error) {
	wallet, err := ctx.GetWallet()
	if err != nil {
		return err
	}

	for symbol, amount := range wallet.Info.Funds.Free {
		a, err := strconv.ParseFloat(amount, 64)

		if err != nil {
			return err
		}

		if a < 0.000001 {
			continue
		}

		_, err = ctx.oc.PostAccountTransfer(
			symbol,
			6,
			1,
			float32(a),
			nil,
		)
		if err != nil {
			return err
		}
	}

	return
}

func (ctx *OKExSpot) GetAccount() (*Account, error) {
	//transfer moune from wallet to account
	ctx.Transfer()

	postData := url.Values{}
	err := ctx.buildPostForm(&postData)
	if err != nil {
		return nil, err
	}

	body, err := HttpPostForm(ctx.client, ctx.api_base_url+url_userinfo, postData)
	if err != nil {
		return nil, err
	}

	var respMap map[string]interface{}

	err = json.Unmarshal(body, &respMap)
	if err != nil {
		return nil, err
	}

	if errcode, isok := respMap["error_code"].(float64); isok {
		errcodeStr := strconv.FormatFloat(errcode, 'f', 0, 64)
		return nil, errors.New(errcodeStr)
	}
	//log.Println(respMap)
	info, ok := respMap["info"].(map[string]interface{})
	if !ok {
		return nil, errors.New(string(body))
	}

	funds := info["funds"].(map[string]interface{})
	free := funds["free"].(map[string]interface{})
	freezed := funds["freezed"].(map[string]interface{})

	account := new(Account)
	account.Exchange = ctx.GetExchangeName()

	account.SubAccounts = make(map[Currency]SubAccount, 6)
	for k, v := range free {
		currencyKey := NewCurrency(k, "")
		subAcc := SubAccount{
			Currency:     currencyKey,
			Amount:       ToFloat64(v),
			ForzenAmount: ToFloat64(freezed[k])}
		account.SubAccounts[currencyKey] = subAcc
	}

	return account, nil
}

//
func (okSpot *OKExSpot) GzipDecode(in []byte) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(in))
	defer reader.Close()

	return ioutil.ReadAll(reader)

}

func (okSpot *OKExSpot) MessageDecode(msg []byte) {

}

func (okSpot *OKExSpot) createWsConn() {
	if okSpot.ws == nil {
		//connect wsx
		okSpot.createWsLock.Lock()
		defer okSpot.createWsLock.Unlock()

		if okSpot.ws == nil {
			okSpot.ws = NewWsConn("wss://real.okex.com:10440/ws/v1")
			okSpot.ws.Heartbeat(func() interface{} { return map[string]string{"event": "ping"} }, 20*time.Second)
			okSpot.ws.ReConnect()
			okSpot.ws.ReceiveMessage(func(msg []byte) {
				var err error
				msg, err = okSpot.GzipDecode(msg)
				if err != nil {
					log.Println(err)
					return
				}

				if string(msg) == "{\"event\":\"pong\"}" {
					okSpot.ws.UpdateActivedTime()
					return
				}

				var data []interface{}
				err = json.Unmarshal(msg, &data)
				if err != nil {
					log.Println(err)
					return
				}

				if len(data) == 0 {
					return
				}

				jsonparser.ArrayEach(msg, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
					channel, err := jsonparser.GetString(value, "channel")
					if err != nil {
						fmt.Printf("channel err :%s \n", err)
						return
					}

					m, _, _, err := jsonparser.Get(value, "data")
					if err != nil {
						fmt.Printf("data err :%s \n", err)
						return
					}

					if channel == "addChannel" {
						fmt.Printf("msg: %s \n", m)
						return
					}

					pair := okSpot.getPairFormChannel(channel)

					if strings.Contains(channel, "_deals") {
						jsonparser.ArrayEach(m, func(value []byte, dataType jsonparser.ValueType, offset int, err error) {
							var t []string
							err = json.Unmarshal(value, &t)
							if err != nil {
								fmt.Printf("tradeArr  Unmarshal err :%s \n", err)
								return
							}

							trade := okSpot.parseTrade(t)
							trade.Pair = pair

							okSpot.wsTradeHandleMap[channel](trade)
						})
						return
					}

					tickmap := make(map[string]interface{})
					err = json.Unmarshal(m, &tickmap)

					if strings.HasSuffix(channel, "_ticker") {
						ticker := okSpot.parseTicker(tickmap)
						ticker.Pair = pair
						okSpot.wsTickerHandleMap[channel](ticker)
					} else if strings.Contains(channel, "depth_") {
						dep := okSpot.parseDepth(tickmap)
						dep.Pair = pair
						okSpot.wsDepthHandleMap[channel](dep)
					}

				})

			})
		}
	}
}

func (okSpot *OKExSpot) GetDepthWithWs(pair CurrencyPair, handle func(*Depth)) error {
	okSpot.createWsConn()
	channel := fmt.Sprintf("ok_sub_spot_%s_depth_5", strings.ToLower(pair.ToSymbol("_")))
	okSpot.wsDepthHandleMap[channel] = handle
	return okSpot.ws.Subscribe(map[string]string{
		"event":   "addChannel",
		"channel": channel})
}

func (okSpot *OKExSpot) GetTickerWithWs(pair CurrencyPair, handle func(*Ticker)) error {
	okSpot.createWsConn()
	channel := fmt.Sprintf("ok_sub_spot_%s_ticker", strings.ToLower(pair.ToSymbol("_")))
	okSpot.wsTickerHandleMap[channel] = handle
	return okSpot.ws.Subscribe(map[string]string{
		"event":   "addChannel",
		"channel": channel})
}

func (okSpot *OKExSpot) GetTradeWithWs(pair CurrencyPair, handle func(*Trade)) error {
	okSpot.createWsConn()
	channel := fmt.Sprintf("ok_sub_spot_%s_deals", strings.ToLower(pair.String()))
	fmt.Printf("channel:%s \n", channel)
	okSpot.wsTradeHandleMap[channel] = handle
	return okSpot.ws.Subscribe(map[string]string{
		"event":   "addChannel",
		"channel": channel})
}

func (okSpot *OKExSpot) parseTrade(arr []string) *Trade {

	trade := new(Trade)
	trade.Tid = int64(ToUint64(arr[0]))
	trade.Price = ToFloat64(arr[1])
	trade.Amount = ToFloat64(arr[2])
	trade.Date = okSpot.formatTimeMs(arr[3])

	if arr[4] == "ask" {
		trade.Type = BUY
	} else {
		trade.Type = SELL
	}

	return trade
}

// date = 15:04:05   return ms
func (okSpot *OKExSpot) formatTimeMs(date string) int64 {
	const format = "2006-01-02 15:04:05"
	day := time.Now().Format("2006-01-02")
	local, _ := time.LoadLocation("Asia/Chongqing")
	t, _ := time.ParseInLocation(format, day+" "+date, local)

	return t.UnixNano() / 1e6

}

func (okSpot *OKExSpot) parseTicker(tickmap map[string]interface{}) *Ticker {
	return &Ticker{
		Last: ToFloat64(tickmap["last"]),
		Low:  ToFloat64(tickmap["low"]),
		High: ToFloat64(tickmap["high"]),
		Vol:  ToFloat64(tickmap["vol"]),
		Sell: ToFloat64(tickmap["sell"]),
		Buy:  ToFloat64(tickmap["buy"]),
		Date: ToUint64(tickmap["timestamp"])}
}

func (okSpot *OKExSpot) parseDepth(tickmap map[string]interface{}) *Depth {
	asks := tickmap["asks"].([]interface{})
	bids := tickmap["bids"].([]interface{})

	var depth Depth
	for _, v := range asks {
		var dr DepthRecord
		for i, vv := range v.([]interface{}) {
			switch i {
			case 0:
				dr.Price = ToFloat64(vv)
			case 1:
				dr.Amount = ToFloat64(vv)
			}
		}
		depth.AskList = append(depth.AskList, dr)
	}

	for _, v := range bids {
		var dr DepthRecord
		for i, vv := range v.([]interface{}) {
			switch i {
			case 0:
				dr.Price = ToFloat64(vv)
			case 1:
				dr.Amount = ToFloat64(vv)
			}
		}
		depth.BidList = append(depth.BidList, dr)
	}
	return &depth
}

func (okSpot *OKExSpot) getPairFormChannel(channel string) CurrencyPair {
	metas := strings.Split(channel, "_")
	return NewCurrencyPair2(metas[3] + "_" + metas[4])
}

type wallet struct {
	Info struct {
		Funds struct {
			Free  map[string]string `json:"free"`
			Holds map[string]string `json:"holds"`
		} `json:"funds"`
	} `json:"info"`
}

/*
 Get a iso time
  eg: 2018-03-16T18:02:48.284Z
*/
func IsoTime() string {
	utcTime := time.Now().UTC()
	iso := utcTime.String()
	isoBytes := []byte(iso)
	iso = string(isoBytes[:10]) + "T" + string(isoBytes[11:23]) + "Z"
	return iso
}

func doParamSign(httpMethod, apiSecret, uri, requestBody string) (string, string) {
	timestamp := IsoTime()
	preText := fmt.Sprintf("%s%s%s%s", timestamp, strings.ToUpper(httpMethod), uri, requestBody)
	log.Println("preHash", preText)
	sign, _ := GetParamHmacSHA256Base64Sign(apiSecret, preText)
	return sign, timestamp
}
