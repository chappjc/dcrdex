// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package auth

import (
	"encoding/json"
	"time"

	"decred.org/dcrdex/dex/encode"
	"decred.org/dcrdex/dex/msgjson"
	"decred.org/dcrdex/server/account"
	"decred.org/dcrdex/server/coinwaiter"
	"decred.org/dcrdex/server/comms"
)

var (
	// The coin waiters will query for transaction data every recheckInterval.
	recheckInterval = time.Second * 5
	// txWaitExpiration is the longest the AuthManager will wait for a coin
	// waiter. This could be thought of as the maximum allowable backend latency.
	txWaitExpiration = time.Minute
)

// handleRegister handles requests to the 'register' route.
func (auth *AuthManager) handleRegister(conn comms.Link, msg *msgjson.Message) *msgjson.Error {
	// Unmarshal.
	register := new(msgjson.Register)
	err := json.Unmarshal(msg.Payload, &register)
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.RPCParseError,
			Message: "error parsing register: " + err.Error(),
		}
	}

	// Create account.Account from pubkey.
	acct, err := account.NewAccountFromPubKey(register.PubKey)
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.PubKeyParseError,
			Message: "error parsing pubkey: " + err.Error(),
		}
	}

	// Check signature.
	sigMsg, err := register.Serialize()
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.RPCParseError,
			Message: "error serializing register: " + err.Error(),
		}
	}
	err = checkSigS256(sigMsg, register.SigBytes(), acct.PubKey)
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.SignatureError,
			Message: "signature error: " + err.Error(),
		}
	}

	// Register account and get a fee payment address.
	feeAddr, err := auth.storage.CreateAccount(acct)
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.RPCInternalError,
			Message: "storage error: " + err.Error(),
		}
	}

	// Prepare, sign, and send response.
	regRes := &msgjson.RegisterResult{
		DEXPubKey:    auth.signer.PubKey().SerializeCompressed(),
		ClientPubKey: register.PubKey,
		Address:      feeAddr,
		Fee:          auth.regFee,
		Time:         encode.UnixMilliU((unixMsNow())),
	}

	err = auth.Sign(regRes)
	if err != nil {
		log.Errorf("error serializing register result: %v", err)
		return &msgjson.Error{
			Code:    msgjson.RPCInternalError,
			Message: "internal error",
		}
	}

	resp, err := msgjson.NewResponse(msg.ID, regRes, nil)
	if err != nil {
		log.Errorf("error creating new response for registration result: %v", err)
		return &msgjson.Error{
			Code:    msgjson.RPCInternalError,
			Message: "internal error",
		}
	}

	err = conn.Send(resp)
	if err != nil {
		log.Warnf("error sending register result to link: %v", err)
	}

	return nil
}

// handleNotifyFee handles requests to the 'notifyfee' route.
func (auth *AuthManager) handleNotifyFee(conn comms.Link, msg *msgjson.Message) *msgjson.Error {
	// Unmarshal.
	notifyFee := new(msgjson.NotifyFee)
	err := json.Unmarshal(msg.Payload, &notifyFee)
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.RPCParseError,
			Message: "error parsing notifyfee: " + err.Error(),
		}
	}

	log.Debugf("notifyfee payload: %x, %x, %x, %d", notifyFee.AccountID, notifyFee.CoinID, notifyFee.Sig, notifyFee.Time)

	// Get account information.
	if len(notifyFee.AccountID) != account.HashSize {
		return &msgjson.Error{
			Code:    msgjson.AuthenticationError,
			Message: "invalid account ID: " + notifyFee.AccountID.String(),
		}
	}

	var acctID account.AccountID
	copy(acctID[:], notifyFee.AccountID)
	acct, paid, open := auth.storage.Account(acctID)
	log.Debugf("Account %x status: found=%v, paid=%v, open=%v", acctID, acct != nil, paid, open)
	if acct == nil {
		return &msgjson.Error{
			Code:    msgjson.AuthenticationError,
			Message: "no account found for ID " + notifyFee.AccountID.String(),
		}
	}
	if !open {
		log.Debugf("Account %x closed", acctID)
		return &msgjson.Error{
			Code:    msgjson.AuthenticationError,
			Message: "account closed and cannot be reopen",
		}
	}
	if paid {
		log.Debugf("Account %x already paid", acctID)
		return &msgjson.Error{
			Code:    msgjson.AuthenticationError,
			Message: "'notifyfee' send for paid account",
		}
	}

	// Check signature
	sigMsg, err := notifyFee.Serialize()
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.RPCParseError,
			Message: "error serializing notifyfee: " + err.Error(),
		}
	}
	err = checkSigS256(sigMsg, notifyFee.SigBytes(), acct.PubKey)
	if err != nil {
		log.Debugf("Account %x invalid notifyfee signature: %v", acctID, err)
		return &msgjson.Error{
			Code:    msgjson.SignatureError,
			Message: "signature error: " + err.Error(),
		}
	}

	// Get the registration fee address assigned to the client's account.
	regAddr, err := auth.storage.AccountRegAddr(acctID)
	log.Debugf("Account %x registration fee address: %v", acctID, regAddr)
	if err != nil {
		return &msgjson.Error{
			Code:    msgjson.RPCInternalError,
			Message: "error locating account info: " + err.Error(),
		}
	}

	auth.coinWaiter.Wait(coinwaiter.NewSettings(acctID, msg, notifyFee.CoinID, txWaitExpiration), func() bool {
		// Validate fee.
		log.Debugf("checking fee from coin %x", notifyFee.CoinID)
		addr, val, confs, err := auth.checkFee(notifyFee.CoinID)
		if err != nil || confs < auth.feeConfs {
			log.Debugf("Failed to check fee: confs=%d, err=%v", confs, err)
			return coinwaiter.TryAgain
		}
		var msgErr *msgjson.Error
		defer func() {
			if msgErr != nil {
				log.Debugf("Sending notifyfee response ERROR: %v", msgErr.Message)
				resp, err := msgjson.NewResponse(msg.ID, nil, msgErr)
				if err != nil {
					log.Errorf("error encoding notifyfee error response: %v", err)
					return
				}
				err = conn.Send(resp)
				if err != nil {
					log.Warnf("error sending notifyfee result to link: %v", err)
				}
			}
		}()
		if val < auth.regFee {
			msgErr = &msgjson.Error{
				Code:    msgjson.FeeError,
				Message: "fee too low",
			}
			return coinwaiter.DontTryAgain
		}
		if addr != regAddr {
			msgErr = &msgjson.Error{
				Code:    msgjson.FeeError,
				Message: "wrong fee address. wanted " + regAddr + " got " + addr,
			}
			return coinwaiter.DontTryAgain
		}

		// Mark the account as paid
		err = auth.storage.PayAccount(acctID, notifyFee.CoinID)
		if err != nil {
			msgErr = &msgjson.Error{
				Code:    msgjson.RPCInternalError,
				Message: "storage.PayAccount failed: " + err.Error(),
			}
			return coinwaiter.DontTryAgain
		}

		log.Infof("New user registered: %v", acctID)

		// Create, sign, and send the the response.
		err = auth.Sign(notifyFee)
		if err != nil {
			msgErr = &msgjson.Error{
				Code:    msgjson.RPCInternalError,
				Message: "internal signature error",
			}
			return coinwaiter.DontTryAgain
		}
		notifyRes := new(msgjson.NotifyFeeResult)
		notifyRes.SetSig(notifyFee.SigBytes())
		resp, err := msgjson.NewResponse(msg.ID, notifyRes, nil)
		if err != nil {
			msgErr = &msgjson.Error{
				Code:    msgjson.RPCInternalError,
				Message: "internal encoding error",
			}
			return coinwaiter.DontTryAgain
		}
		log.Debugf("Sending successful notifyfee response: %v", resp.String())
		err = conn.Send(resp)
		if err != nil {
			log.Warnf("error sending notifyfee result to link: %v", err)
		}
		return coinwaiter.DontTryAgain
	})
	return nil
}
