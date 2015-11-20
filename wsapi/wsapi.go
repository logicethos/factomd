// Copyright 2015 Factom Foundation
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package wsapi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/FactomProject/factomd/common/interfaces"
	"github.com/FactomProject/factomd/common/constants"
	"github.com/FactomProject/factomd/common/factoid"
	"github.com/FactomProject/factomd/common/messages"
	"github.com/FactomProject/factomd/log"
	"github.com/hoisie/web"
)

const (
	httpOK  = 200
	httpBad = 400
)

var Servers map[int]*web.Server

func Start(state interfaces.IState) {
	
	var server *web.Server
	
	if Servers == nil {
		Servers = make(map[int]*web.Server)
	}
	
	if Servers[state.GetPort()] == nil {
		server = web.NewServer()
		Servers[state.GetPort()] = server
		server.Env["state"] = state
		
		server.Post("/v1/factoid-submit/?", handleFactoidSubmit)
		server.Get("/v1/factoid-balance/([^/]+)", handleFactoidBalance)

		log.Print("Starting server")
		go server.Run(fmt.Sprintf("localhost:%d", state.GetPort()))
	}

	
}

func Stop(state interfaces.IState) {
	Servers[state.GetPort()].Close()
}

func handleFactoidSubmit(ctx *web.Context) {

	state := ctx.Server.Env["state"].(interfaces.IState)

	type x struct{ Transaction string }
	t := new(x)

	var p []byte
	var err error
	if p, err = ioutil.ReadAll(ctx.Request.Body); err != nil {
		wsLog.Error(err)
		returnMsg(ctx, "Unable to read the request", false)
		return
	} else {
		if err := json.Unmarshal(p, t); err != nil {
			returnMsg(ctx, "Unable to Unmarshal the request", false)
			return
		}
	}

	msg := new(messages.FactoidTransaction)

	if p, err = hex.DecodeString(t.Transaction); err != nil {
		returnMsg(ctx, "Unable to decode the transaction", false)
		return
	}

	err = msg.UnmarshalBinary(p)

	if err != nil {
		returnMsg(ctx, err.Error(), false)
		return
	}

	err = state.GetFactoidState().Validate(1, msg.Transaction)

	if err != nil {
		returnMsg(ctx, err.Error(), false)
		return
	}

	state.NetworkInMsgQueue() <- msg

	returnMsg(ctx, "Successfully submitted the transaction", true)

}

func handleFactoidBalance(ctx *web.Context, eckey string) {
	
	state := ctx.Server.Env["state"].(interfaces.IState)
	
	type fbal struct {
		Response string
		Success  bool
	}
	var b fbal
	adr, err := hex.DecodeString(eckey)
	if err == nil && len(adr) != constants.HASH_LENGTH {
		b = fbal{Response: "Invalid Address", Success: false}
	}
	if err == nil {
		v := int64(state.GetFactoidState().GetBalance(factoid.NewAddress(adr).Fixed()))
		str := fmt.Sprintf("%d", v)
		b = fbal{Response: str, Success: true}
	} else {
		b = fbal{Response: err.Error(), Success: false}
	}
	
	if p, err := json.Marshal(b); err != nil {
		wsLog.Error(err)
		return
	} else {
		ctx.Write(p)
	}
	
}


/*********************************************************
 * Support Functions
 *********************************************************/

func returnMsg(ctx *web.Context, msg string, success bool) {
	type rtn struct {
		Response string
		Success  bool
	}
	r := rtn{Response: msg, Success: success}

	if p, err := json.Marshal(r); err != nil {
		wsLog.Error(err)
		return
	} else {
		ctx.Write(p)
	}
}
