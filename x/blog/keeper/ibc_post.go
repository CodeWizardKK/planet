package keeper

import (
	"errors"
	"strconv"

	"planet/x/blog/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	clienttypes "github.com/cosmos/ibc-go/v2/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v2/modules/core/04-channel/types"
	host "github.com/cosmos/ibc-go/v2/modules/core/24-host"
)

// 送信する
// IBC経由でパケットを送信するために手動で呼び出されます。
// このメソッドは、パケットがIBC経由で別のブロックチェーンアプリに送信される前のロジックも定義します。
// 指定されたソースポートとソースチャネルを使用してIBC経由でパケットを送信します。
func (k Keeper) TransmitIbcPostPacket(
	ctx sdk.Context,
	packetData types.IbcPostPacketData,
	sourcePort,
	sourceChannel string,
	timeoutHeight clienttypes.Height,
	timeoutTimestamp uint64,
) error {

	sourceChannelEnd, found := k.ChannelKeeper.GetChannel(ctx, sourcePort, sourceChannel)
	if !found {
		return sdkerrors.Wrapf(channeltypes.ErrChannelNotFound, "port ID (%s) channel ID (%s)", sourcePort, sourceChannel)
	}

	destinationPort := sourceChannelEnd.GetCounterparty().GetPortID()
	destinationChannel := sourceChannelEnd.GetCounterparty().GetChannelID()

	// get the next sequence
	sequence, found := k.ChannelKeeper.GetNextSequenceSend(ctx, sourcePort, sourceChannel)
	if !found {
		return sdkerrors.Wrapf(
			channeltypes.ErrSequenceSendNotFound,
			"source port: %s, source channel: %s", sourcePort, sourceChannel,
		)
	}

	channelCap, ok := k.ScopedKeeper.GetCapability(ctx, host.ChannelCapabilityPath(sourcePort, sourceChannel))
	if !ok {
		return sdkerrors.Wrap(channeltypes.ErrChannelCapabilityNotFound, "module does not own channel capability")
	}

	packetBytes, err := packetData.GetBytes()
	if err != nil {
		return sdkerrors.Wrap(sdkerrors.ErrJSONMarshal, "cannot marshal the packet: "+err.Error())
	}

	packet := channeltypes.NewPacket(
		packetBytes,
		sequence,
		sourcePort,
		sourceChannel,
		destinationPort,
		destinationChannel,
		timeoutHeight,
		timeoutTimestamp,
	)

	if err := k.ChannelKeeper.SendPacket(ctx, channelCap, packet); err != nil {
		return err
	}

	return nil
}

// 受信時
// パケットがチェーンで受信されると自動的に呼び出され、パケット受信ロジックを定義します。
// パケット受信を処理します
func (k Keeper) OnRecvIbcPostPacket(ctx sdk.Context, packet channeltypes.Packet, data types.IbcPostPacketData) (packetAck types.IbcPostPacketAck, err error) {
	// validate packet data upon receiving
	if err := data.ValidateBasic(); err != nil {
		return packetAck, err
	}

	// 投稿メッセージを受信したら、受信チェーンにタイトルとコンテンツを含む新しい投稿を作成する。
	// AppendPost：新しく追加された投稿のIDを返します。この値は、承認によってソースチェーンに返すことができる。
	id := k.AppendPost(
		ctx,
		types.Post{
			//メッセージの発信元であるブロックチェーンアプリとメッセージの作成者を特定するには、<portID>-<channelID>-<creatorAddress>
			Creator: packet.SourcePort + "-" + packet.SourceChannel + "-" + data.Creator,
			Title:   data.Title,
			Content: data.Content,
		},
	)

	packetAck.PostID = strconv.FormatUint(id, 10)

	return packetAck, nil
}

// パケットがチェーンで受信されると自動的に呼び出され、パケット受信ロジックを定義します。
// パケットの成功または失敗に応答します
// 受信チェーンに書かれた承認
func (k Keeper) OnAcknowledgementIbcPostPacket(ctx sdk.Context, packet channeltypes.Packet, data types.IbcPostPacketData, ack channeltypes.Acknowledgement) error {
	switch dispatchedAck := ack.Response.(type) {
	case *channeltypes.Acknowledgement_Error:

		// TODO: failed acknowledgement logic
		_ = dispatchedAck.Error

		return nil
	case *channeltypes.Acknowledgement_Result:
		// Decode the packet acknowledgment
		var packetAck types.IbcPostPacketAck

		if err := types.ModuleCdc.UnmarshalJSON(dispatchedAck.Result, &packetAck); err != nil {
			// The counter-party module doesn't implement the correct acknowledgment format
			return errors.New("cannot unmarshal acknowledgment")
		}

		//送信ブロックチェーンにsentPostを保存して、ターゲットチェーンで投稿が受信されたことを確認します。
		//投稿を識別するためのタイトルとターゲットを格納
		k.AppendSentPost(
			ctx,
			types.SentPost{
				Creator: data.Creator,
				PostID:  packetAck.PostID,
				Title:   data.Title,
				Chain:   packet.DestinationPort + "-" + packet.DestinationChannel,
			},
		)

		return nil
	default:
		// The counter-party module doesn't implement the correct acknowledgment format
		return errors.New("the counter-party module does not implement the correct acknowledgment format")
	}
}

// 送信されたパケットがタイムアウトすると、フックが呼び出されます。パケットがターゲットチェーンで受信されない場合のロジックを定義します。
// タイムアウトのためにパケットが送信されなかった場合に応答します
func (k Keeper) OnTimeoutIbcPostPacket(ctx sdk.Context, packet channeltypes.Packet, data types.IbcPostPacketData) error {

	//ターゲットチェーンによって受信されていない投稿をtimedoutPost投稿に格納する
	k.AppendTimedoutPost(
		ctx,
		types.TimedoutPost{
			Title:   data.Title,
			Creator: data.Creator,
			Chain:   packet.DestinationPort + "-" + packet.DestinationChannel,
		},
	)

	return nil
}
