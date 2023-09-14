package brc20

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/unisat-wallet/libbrc20-indexer/constant"
	"github.com/unisat-wallet/libbrc20-indexer/decimal"
	"github.com/unisat-wallet/libbrc20-indexer/model"
	"github.com/unisat-wallet/libbrc20-indexer/utils"
)

func ProcessUpdateLatestBRC20(brc20Datas []*model.InscriptionBRC20Data) (inscriptionsTickerInfoMap map[string]*model.BRC20TokenInfo,
	userTokensBalanceData map[string]map[string]*model.BRC20TokenBalance,
	tokenUsersBalanceData map[string]map[string]*model.BRC20TokenBalance,
	inscriptionsValidBRC20DataMap map[string]model.InscriptionBRC20InfoResp,
) {
	totalDataCount := len(brc20Datas)
	log.Printf("ProcessUpdateLatestBRC20 update. total %d", totalDataCount)

	inscriptionsTickerInfoMap = make(map[string]*model.BRC20TokenInfo, 0)
	userTokensBalanceData = make(map[string]map[string]*model.BRC20TokenBalance, 0)
	tokenUsersBalanceData = make(map[string]map[string]*model.BRC20TokenBalance, 0)
	inscriptionsValidBRC20DataMap = make(map[string]model.InscriptionBRC20InfoResp, 0)

	inscriptionsValidTransferMap := make(map[string]*model.InscriptionBRC20TickInfo, 0)
	inscriptionsInvalidTransferMap := make(map[string]*model.InscriptionBRC20TickInfo, 0)

	for idx, data := range brc20Datas {
		// is sending transfer
		if data.IsTransfer {

			isInvalid := false
			validTransferInfo, ok := inscriptionsValidTransferMap[data.CreateIdxKey]
			if !ok {
				validTransferInfo, ok = inscriptionsInvalidTransferMap[data.CreateIdxKey]
				if !ok {
					continue
				} else {
					delete(inscriptionsInvalidTransferMap, data.CreateIdxKey)
				}
				isInvalid = true
			} else {
				delete(inscriptionsValidTransferMap, data.CreateIdxKey)
			}
			// ticker
			uniqueLowerTicker := strings.ToLower(validTransferInfo.Data.BRC20Tick)
			tokenInfo, ok := inscriptionsTickerInfoMap[uniqueLowerTicker]
			if !ok {
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) send transfer, but ticker invalid. txid: %s",
					idx*100/totalDataCount,
					utils.GetReversedStringHex(data.TxId),
				)
				continue
			}

			// global history
			history := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_TRANSFER, !isInvalid, true, validTransferInfo, nil, data)
			tokenInfo.History = append(tokenInfo.History, history)
			tokenInfo.HistoryTransfer = append(tokenInfo.HistoryTransfer, history)

			// from
			// get user's tokens to update
			fromUserTokens, ok := userTokensBalanceData[string(validTransferInfo.PkScript)]
			if !ok {
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) send from user missing. height: %d, txidx: %d",
					idx*100/totalDataCount,
					data.Height,
					data.TxIdx,
				)
				continue
			}
			// get tokenBalance to update
			fromTokenBalance, ok := fromUserTokens[uniqueLowerTicker]
			if !ok {
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) send from ticker missing. height: %d, txidx: %d",
					idx*100/totalDataCount,
					data.Height,
					data.TxIdx,
				)
				continue
			}

			if isInvalid {
				fromHistory := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_SEND, false, true, validTransferInfo, fromTokenBalance, data)
				fromTokenBalance.History = append(fromTokenBalance.History, fromHistory)
				fromTokenBalance.HistorySend = append(fromTokenBalance.HistorySend, fromHistory)
				continue
			}

			if _, ok := fromTokenBalance.ValidTransferMap[data.CreateIdxKey]; !ok {
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) send from transfer missing(dup transfer?). height: %d, txidx: %d",
					idx*100/totalDataCount,
					data.Height,
					data.TxIdx,
				)
				continue
			}

			// to
			// get user's tokens to update
			var userTokens map[string]*model.BRC20TokenBalance
			if tokens, ok := userTokensBalanceData[string(data.PkScript)]; !ok {
				userTokens = make(map[string]*model.BRC20TokenBalance, 0)
				userTokensBalanceData[string(data.PkScript)] = userTokens
			} else {
				userTokens = tokens
			}
			// get tokenBalance to update
			var tokenBalance *model.BRC20TokenBalance
			if token, ok := userTokens[uniqueLowerTicker]; !ok {
				tokenBalance = &model.BRC20TokenBalance{Ticker: validTransferInfo.Data.BRC20Tick, PkScript: data.PkScript}
				userTokens[uniqueLowerTicker] = tokenBalance

				// set token's users
				tokenUsers := tokenUsersBalanceData[uniqueLowerTicker]
				tokenUsers[string(data.PkScript)] = tokenBalance
			} else {
				tokenBalance = token
			}

			// set from
			fromTokenBalance.OverallBalanceSafe = fromTokenBalance.OverallBalanceSafe.Sub(validTransferInfo.Amount)
			fromTokenBalance.OverallBalance = fromTokenBalance.OverallBalance.Sub(validTransferInfo.Amount)
			fromTokenBalance.TransferableBalance = fromTokenBalance.TransferableBalance.Sub(validTransferInfo.Amount)
			delete(fromTokenBalance.ValidTransferMap, data.CreateIdxKey)

			fromHistory := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_SEND, true, true, validTransferInfo, fromTokenBalance, data)
			fromTokenBalance.History = append(fromTokenBalance.History, fromHistory)
			fromTokenBalance.HistorySend = append(fromTokenBalance.HistorySend, fromHistory)

			// set to
			if data.BlockTime > 0 {
				tokenBalance.OverallBalanceSafe = tokenBalance.OverallBalanceSafe.Add(validTransferInfo.Amount)
			}
			tokenBalance.OverallBalance = tokenBalance.OverallBalance.Add(validTransferInfo.Amount)

			toHistory := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_RECEIVE, true, true, validTransferInfo, tokenBalance, data)
			tokenBalance.History = append(tokenBalance.History, toHistory)
			tokenBalance.HistoryReceive = append(tokenBalance.HistoryReceive, toHistory)

			continue
		}

		// is inscribe deploy/mint/transfer
		var bodyMap map[string]interface{} = make(map[string]interface{}, 8)
		if err := json.Unmarshal([]byte(data.ContentBody), &bodyMap); err != nil {
			log.Printf("ProcessUpdateLatestBRC20 (%d %%) parse json, but failed. txid: %s",
				idx*100/totalDataCount,
				utils.GetReversedStringHex(data.TxId),
			)
			continue
		}
		data.ContentBody = ""
		var body model.InscriptionBRC20Content
		if v, ok := bodyMap["p"].(string); ok {
			body.Proto = v
		}
		if v, ok := bodyMap["op"].(string); ok {
			body.Operation = v
		}
		if v, ok := bodyMap["tick"].(string); ok {
			body.BRC20Tick = v
		}
		if v, ok := bodyMap["max"].(string); ok {
			body.BRC20Max = v
		}
		if v, ok := bodyMap["amt"].(string); ok {
			body.BRC20Amount = v
		}

		if _, ok := bodyMap["lim"]; !ok {
			body.BRC20Limit = body.BRC20Max
		} else {
			if v, ok := bodyMap["lim"].(string); ok {
				body.BRC20Limit = v
			}
		}

		if _, ok := bodyMap["dec"]; !ok {
			body.BRC20Decimal = "18"
		} else {
			if v, ok := bodyMap["dec"].(string); ok {
				body.BRC20Decimal = v
			}
		}

		if body.Proto != "brc-20" || len(body.BRC20Tick) != 4 {
			continue
		}

		uniqueLowerTicker := strings.ToLower(body.BRC20Tick)
		if body.Operation == constant.BRC20_OP_DEPLOY {
			if _, ok := inscriptionsTickerInfoMap[uniqueLowerTicker]; ok { // dup ticker
				continue
			}
			if body.BRC20Max == "" { // without max
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) deploy, but max missing. ticker: %s",
					idx*100/totalDataCount,
					uniqueLowerTicker,
				)
				continue
			}

			tinfo := model.NewInscriptionBRC20TickInfo(&body, data)
			tinfo.Data.BRC20Max = body.BRC20Max
			tinfo.Data.BRC20Limit = body.BRC20Limit
			tinfo.Data.BRC20Decimal = body.BRC20Decimal
			tinfo.Data.BRC20Minted = "0"
			tinfo.InscriptionNumberStart = data.InscriptionNumber

			// dec
			if dec, err := strconv.ParseUint(tinfo.Data.BRC20Decimal, 10, 64); err != nil || dec > 18 {
				// dec invalid
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) deploy, but dec invalid. ticker: %s, dec: %s",
					idx*100/totalDataCount,
					uniqueLowerTicker,
					tinfo.Data.BRC20Decimal,
				)
				continue
			} else {
				tinfo.Decimal = uint8(dec)
			}

			// max
			if max, precision, err := decimal.NewDecimalFromString(body.BRC20Max); err != nil {
				// max invalid
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) deploy, but max invalid. ticker: %s, max: '%s'",
					idx*100/totalDataCount,
					uniqueLowerTicker,
					body.BRC20Max,
				)
				continue
			} else {
				if max.Sign() <= 0 || max.IsOverflowUint64() || precision > int(tinfo.Decimal) {
					continue
				}
				tinfo.Max = max
			}

			// lim
			if lim, precision, err := decimal.NewDecimalFromString(tinfo.Data.BRC20Limit); err != nil {
				// limit invalid
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) deploy, but limit invalid. ticker: %s, limit: '%s'",
					idx*100/totalDataCount,
					uniqueLowerTicker,
					tinfo.Data.BRC20Limit,
				)
				continue
			} else {
				if lim.Sign() <= 0 || lim.IsOverflowUint64() || precision > int(tinfo.Decimal) {
					continue
				}
				tinfo.Limit = lim
			}

			tokenInfo := &model.BRC20TokenInfo{Ticker: body.BRC20Tick, Deploy: tinfo}
			inscriptionsTickerInfoMap[uniqueLowerTicker] = tokenInfo

			tokenBalance := &model.BRC20TokenBalance{Ticker: body.BRC20Tick, PkScript: data.PkScript}

			history := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_INSCRIBE_DEPLOY, true, false, tinfo, nil, data)
			tokenBalance.History = append(tokenBalance.History, history)
			tokenInfo.History = append(tokenInfo.History, history)

			// init user tokens
			var userTokens map[string]*model.BRC20TokenBalance
			if tokens, ok := userTokensBalanceData[string(data.PkScript)]; !ok {
				userTokens = make(map[string]*model.BRC20TokenBalance, 0)
				userTokensBalanceData[string(data.PkScript)] = userTokens
			} else {
				userTokens = tokens
			}
			userTokens[uniqueLowerTicker] = tokenBalance

			// init token users
			tokenUsers := make(map[string]*model.BRC20TokenBalance, 0)
			tokenUsers[string(data.PkScript)] = tokenBalance
			tokenUsersBalanceData[uniqueLowerTicker] = tokenUsers

			inscriptionsValidBRC20DataMap[data.CreateIdxKey] = tinfo.Data

		} else if body.Operation == constant.BRC20_OP_MINT {
			tokenInfo, ok := inscriptionsTickerInfoMap[uniqueLowerTicker]
			if !ok {
				continue
			}
			tinfo := tokenInfo.Deploy

			// check mint amount
			amt, precision, err := decimal.NewDecimalFromString(body.BRC20Amount)
			if err != nil {
				continue
			}
			if precision > int(tinfo.Decimal) {
				continue
			}
			if amt.Sign() <= 0 || amt.Cmp(tinfo.Limit) > 0 {
				continue
			}

			// get user's tokens to update
			var userTokens map[string]*model.BRC20TokenBalance
			if tokens, ok := userTokensBalanceData[string(data.PkScript)]; !ok {
				userTokens = make(map[string]*model.BRC20TokenBalance, 0)
				userTokensBalanceData[string(data.PkScript)] = userTokens
			} else {
				userTokens = tokens
			}
			// get tokenBalance to update
			var tokenBalance *model.BRC20TokenBalance
			if token, ok := userTokens[uniqueLowerTicker]; !ok {
				tokenBalance = &model.BRC20TokenBalance{Ticker: tokenInfo.Ticker, PkScript: data.PkScript}
				userTokens[uniqueLowerTicker] = tokenBalance

				// init token's users
				tokenUsers := tokenUsersBalanceData[uniqueLowerTicker]
				tokenUsers[string(data.PkScript)] = tokenBalance
			} else {
				tokenBalance = token
			}

			body.BRC20Tick = tokenInfo.Ticker
			mintInfo := model.NewInscriptionBRC20TickInfo(&body, data)
			mintInfo.Data.BRC20Amount = body.BRC20Amount
			mintInfo.Data.BRC20Minted = amt.String()
			mintInfo.Decimal = tinfo.Decimal
			mintInfo.Amount = amt
			if tinfo.TotalMinted.Cmp(tinfo.Max) >= 0 {
				// invalid history
				history := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_INSCRIBE_MINT, false, false, mintInfo, tokenBalance, data)
				tokenBalance.History = append(tokenBalance.History, history)
				tokenBalance.HistoryMint = append(tokenBalance.HistoryMint, history)
				tokenInfo.History = append(tokenInfo.History, history)
				tokenInfo.HistoryMint = append(tokenInfo.HistoryMint, history)
				continue
			}

			// update tinfo
			// minted
			balanceMinted := decimal.NewDecimalCopy(amt)
			if tinfo.TotalMinted.Add(amt).Cmp(tinfo.Max) > 0 {
				balanceMinted = tinfo.Max.Sub(tinfo.TotalMinted)
			}
			tinfo.TotalMinted = tinfo.TotalMinted.Add(balanceMinted)
			if tinfo.TotalMinted.Cmp(tinfo.Max) >= 0 {
				tinfo.CompleteHeight = data.Height
				tinfo.CompleteBlockTime = data.BlockTime
			}
			// confirmed minted
			now := time.Now()
			if data.BlockTime > 0 {
				tinfo.ConfirmedMinted = tinfo.ConfirmedMinted.Add(balanceMinted)
				if data.BlockTime < uint32(now.Unix())-3600 {
					tinfo.ConfirmedMinted1h = tinfo.ConfirmedMinted1h.Add(balanceMinted)
				}
				if data.BlockTime < uint32(now.Unix())-86400 {
					tinfo.ConfirmedMinted24h = tinfo.ConfirmedMinted24h.Add(balanceMinted)
				}
			}
			// count
			tinfo.MintTimes++
			tinfo.Data.BRC20Minted = tinfo.TotalMinted.String()
			// valid mint inscriptionNumber range
			tinfo.InscriptionNumberEnd = data.InscriptionNumber

			// update mint info
			mintInfo.Data.BRC20Minted = balanceMinted.String()
			mintInfo.Amount = balanceMinted

			// update tokenBalance
			if data.BlockTime > 0 {
				tokenBalance.OverallBalanceSafe = tokenBalance.OverallBalanceSafe.Add(balanceMinted)
			}
			tokenBalance.OverallBalance = tokenBalance.OverallBalance.Add(balanceMinted)

			// history
			history := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_INSCRIBE_MINT, true, false, mintInfo, tokenBalance, data)
			tokenBalance.History = append(tokenBalance.History, history)
			tokenBalance.HistoryMint = append(tokenBalance.HistoryMint, history)
			tokenInfo.History = append(tokenInfo.History, history)
			tokenInfo.HistoryMint = append(tokenInfo.HistoryMint, history)

			inscriptionsValidBRC20DataMap[data.CreateIdxKey] = mintInfo.Data

		} else if body.Operation == constant.BRC20_OP_TRANSFER {
			tokenInfo, ok := inscriptionsTickerInfoMap[uniqueLowerTicker]
			if !ok {
				continue
			}
			tinfo := tokenInfo.Deploy
			// check amount
			amt, precision, err := decimal.NewDecimalFromString(body.BRC20Amount)
			if err != nil {
				log.Printf("ProcessUpdateLatestBRC20 (%d %%) inscribe transfer, but amount invalid. ticker: %s, amount: '%s'",
					idx*100/totalDataCount,
					tokenInfo.Ticker,
					body.BRC20Amount,
				)
				continue
			}
			if precision > int(tinfo.Decimal) {
				continue
			}
			if amt.Sign() <= 0 || amt.Cmp(tinfo.Max) > 0 {
				continue
			}

			balanceTransfer := decimal.NewDecimalCopy(amt)

			// get user's tokens to update
			var userTokens map[string]*model.BRC20TokenBalance
			if tokens, ok := userTokensBalanceData[string(data.PkScript)]; !ok {
				userTokens = make(map[string]*model.BRC20TokenBalance, 0)
				userTokensBalanceData[string(data.PkScript)] = userTokens
			} else {
				userTokens = tokens
			}
			// get tokenBalance to update
			var tokenBalance *model.BRC20TokenBalance
			if token, ok := userTokens[uniqueLowerTicker]; !ok {
				tokenBalance = &model.BRC20TokenBalance{Ticker: tokenInfo.Ticker, PkScript: data.PkScript}
				userTokens[uniqueLowerTicker] = tokenBalance

				// set token's users
				tokenUsers := tokenUsersBalanceData[uniqueLowerTicker]
				tokenUsers[string(data.PkScript)] = tokenBalance
			} else {
				tokenBalance = token
			}

			body.BRC20Tick = tokenInfo.Ticker
			transferInfo := model.NewInscriptionBRC20TickInfo(&body, data)
			transferInfo.Data.BRC20Amount = body.BRC20Amount
			transferInfo.Data.BRC20Limit = tinfo.Data.BRC20Limit
			transferInfo.Data.BRC20Decimal = tinfo.Data.BRC20Decimal

			transferInfo.Decimal = tinfo.Decimal
			transferInfo.Amount = balanceTransfer

			history := model.NewBRC20History(constant.BRC20_HISTORY_TYPE_N_INSCRIBE_TRANSFER, true, false, transferInfo, tokenBalance, data)
			if tokenBalance.OverallBalance.Sub(tokenBalance.TransferableBalance).Cmp(balanceTransfer) < 0 { // invalid
				history.Valid = false
				// user history
				tokenBalance.History = append(tokenBalance.History, history)
				tokenBalance.HistoryInscribeTransfer = append(tokenBalance.HistoryInscribeTransfer, history)
				// global history
				tokenInfo.History = append(tokenInfo.History, history)
				tokenInfo.HistoryInscribeTransfer = append(tokenInfo.HistoryInscribeTransfer, history)

				tokenBalance.InvalidTransferList = append(tokenBalance.InvalidTransferList, transferInfo)
				inscriptionsInvalidTransferMap[data.CreateIdxKey] = transferInfo
			} else {
				tokenBalance.TransferableBalance = tokenBalance.TransferableBalance.Add(balanceTransfer)
				history.TransferableBalance = tokenBalance.TransferableBalance.String()											// update  balance
				history.AvailableBalance = tokenBalance.OverallBalance.Sub(tokenBalance.TransferableBalance).String() // update  balance

				history.Valid = true
				// user history
				tokenBalance.History = append(tokenBalance.History, history)
				tokenBalance.HistoryInscribeTransfer = append(tokenBalance.HistoryInscribeTransfer, history)
				// global history
				tokenInfo.History = append(tokenInfo.History, history)
				tokenInfo.HistoryInscribeTransfer = append(tokenInfo.HistoryInscribeTransfer, history)

				if tokenBalance.ValidTransferMap == nil {
					tokenBalance.ValidTransferMap = make(map[string]*model.InscriptionBRC20TickInfo, 1)
				}
				tokenBalance.ValidTransferMap[data.CreateIdxKey] = transferInfo
				inscriptionsValidTransferMap[data.CreateIdxKey] = transferInfo
				inscriptionsValidBRC20DataMap[data.CreateIdxKey] = transferInfo.Data
			}

		} else {
			continue
		}

	}

	for _, holdersBalanceMap := range tokenUsersBalanceData {
		for key, balance := range holdersBalanceMap {
			if balance.OverallBalance.Sign() <= 0 {
				delete(holdersBalanceMap, key)
			}
		}
	}

	log.Printf("ProcessUpdateLatestBRC20 finish. ticker: %d, users: %d, tokens: %d, validInscription: %d, validTransfer: %d, invalidTransfer: %d",
		len(inscriptionsTickerInfoMap),
		len(userTokensBalanceData),
		len(tokenUsersBalanceData),

		len(inscriptionsValidBRC20DataMap),
		len(inscriptionsValidTransferMap),
		len(inscriptionsInvalidTransferMap),
	)

	return inscriptionsTickerInfoMap, userTokensBalanceData, tokenUsersBalanceData, inscriptionsValidBRC20DataMap
}
