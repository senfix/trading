package okcoin

import (
	"encoding/json"
	"errors"
	"fmt"
	. "github.com/senfix/trading"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"compress/flate"
	"bytes"
	"io/ioutil"
	"github.com/buger/jsonparser"
)

type OKExSpot struct {
	OKCoinCN_API
	ws                *WsConn
	createWsLock      sync.Mutex
	wsTickerHandleMap map[string]func(*Ticker)
	wsDepthHandleMap  map[string]func(*Depth)
	wsTradeHandleMap  map[string]func(*Trade)
}

func NewOKExSpot(client *http.Client, accesskey, secretkey string) *OKExSpot {
	return &OKExSpot{
		OKCoinCN_API:      OKCoinCN_API{client, accesskey, secretkey, "https://www.okex.com/api/v1/"},
		wsTickerHandleMap: make(map[string]func(*Ticker)),
		wsDepthHandleMap:  make(map[string]func(*Depth)),
		wsTradeHandleMap:  make(map[string]func(*Trade))}
}

func (ctx *OKExSpot) GetExchangeName() string {
	return OKEX
}

func (ctx *OKExSpot) GetAccount() (*Account, error) {
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

				}, )

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
