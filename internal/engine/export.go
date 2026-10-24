// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	enginev1 "github.com/OWNER/ghostreel/gen/engine/v1"
	sc2 "github.com/OWNER/ghostreel/internal/engine/s2api/s2clientprotocol"
	"google.golang.org/protobuf/proto"
)

// ExportOptions control Export.
type ExportOptions struct {
	Step           uint32 // loops between frames; default 16
	ObservedPlayer uint32 // whose orders to record; default 1
	// BeforeStep runs before every step; returning an error aborts. The CLI
	// uses it to back off when the host is busy.
	BeforeStep func(loop uint32) error
	Progress   func(loop, total uint32)
}

// ReplayInfo asks the client about a replay without loading it.
func (c *Client) ReplayInfo(ctx context.Context, replay []byte) (*sc2.ResponseReplayInfo, error) {
	resp, err := c.Call(ctx, &sc2.Request{Request: &sc2.Request_ReplayInfo{ReplayInfo: &sc2.RequestReplayInfo{
		Replay: &sc2.RequestReplayInfo_ReplayData{ReplayData: replay},
	}}})
	if err != nil {
		return nil, err
	}
	ri := resp.GetReplayInfo()
	if ri.Error != nil {
		return ri, fmt.Errorf("replay info: %v %s", ri.GetError(), ri.GetErrorDetails())
	}
	return ri, nil
}

// Export plays a replay in the client and records every unit every Step loops.
// The client must be idle (launched or ended) and of the replay's build.
func Export(ctx context.Context, c *Client, replay []byte, o ExportOptions) (*enginev1.Samples, error) {
	if o.Step == 0 {
		o.Step = 16
	}
	if o.ObservedPlayer == 0 {
		o.ObservedPlayer = 1
	}
	info, err := c.ReplayInfo(ctx, replay)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(replay)
	out := &enginev1.Samples{
		SchemaVersion:  enginev1.SchemaVersion_SCHEMA_VERSION_1,
		ReplayHash:     hex.EncodeToString(sum[:]),
		BaseBuild:      info.GetBaseBuild(),
		GameVersion:    info.GetGameVersion(),
		Step:           o.Step,
		ObservedPlayer: o.ObservedPlayer,
		Loops:          info.GetGameDurationLoops(),
	}

	_, err = c.Call(ctx, &sc2.Request{Request: &sc2.Request_StartReplay{StartReplay: &sc2.RequestStartReplay{
		Replay:           &sc2.RequestStartReplay_ReplayData{ReplayData: replay},
		ObservedPlayerId: proto.Int32(int32(o.ObservedPlayer)),
		DisableFog:       proto.Bool(true),
		Realtime:         proto.Bool(false),
		Options: &sc2.InterfaceOptions{
			Raw:                 proto.Bool(true),
			ShowCloaked:         proto.Bool(true),
			ShowBurrowedShadows: proto.Bool(true),
		},
	}}})
	if err != nil {
		return nil, fmt.Errorf("start replay: %w", err)
	}

	data, err := c.Call(ctx, &sc2.Request{Request: &sc2.Request_Data{Data: &sc2.RequestData{UnitTypeId: proto.Bool(true)}}})
	if err != nil {
		return nil, fmt.Errorf("game data: %w", err)
	}
	for _, u := range data.GetData().GetUnits() {
		if u.GetName() == "" {
			continue
		}
		out.UnitTypes = append(out.UnitTypes, &enginev1.UnitTypeInfo{
			Id: u.GetUnitId(), Name: u.GetName(), MovementSpeed: u.GetMovementSpeed(),
		})
	}

	for {
		resp, err := c.Call(ctx, &sc2.Request{Request: &sc2.Request_Observation{Observation: &sc2.RequestObservation{}}})
		if err != nil {
			return nil, fmt.Errorf("observation: %w", err)
		}
		obs := resp.GetObservation().GetObservation()
		loop := obs.GetGameLoop()
		out.Frames = append(out.Frames, frame(loop, obs.GetRawData().GetUnits()))
		if o.Progress != nil {
			o.Progress(loop, out.Loops)
		}
		if resp.GetStatus() == sc2.Status_ended || len(resp.GetObservation().GetPlayerResult()) > 0 ||
			(out.Loops > 0 && loop >= out.Loops) {
			break
		}
		if o.BeforeStep != nil {
			if err := o.BeforeStep(loop); err != nil {
				return nil, err
			}
		}
		resp, err = c.Call(ctx, &sc2.Request{Request: &sc2.Request_Step{Step: &sc2.RequestStep{Count: proto.Uint32(o.Step)}}})
		if err != nil {
			return nil, fmt.Errorf("step at loop %d: %w", loop, err)
		}
		if resp.GetStatus() == sc2.Status_ended {
			break
		}
	}
	return out, nil
}

func frame(loop uint32, units []*sc2.Unit) *enginev1.Frame {
	f := &enginev1.Frame{Loop: loop, Units: make([]*enginev1.UnitSample, 0, len(units))}
	for _, u := range units {
		s := &enginev1.UnitSample{
			Tag: u.GetTag(), UnitType: u.GetUnitType(), Owner: u.GetOwner(),
			X: u.GetPos().GetX(), Y: u.GetPos().GetY(), Z: u.GetPos().GetZ(),
			Facing: u.GetFacing(), Radius: u.GetRadius(), Flying: u.GetIsFlying(),
			BuildProgress: u.GetBuildProgress(),
		}
		for _, ord := range u.GetOrders() {
			o := &enginev1.Order{AbilityId: ord.GetAbilityId(), TargetTag: ord.GetTargetUnitTag()}
			if p := ord.GetTargetWorldSpacePos(); p != nil {
				o.HasPoint, o.TargetX, o.TargetY = true, p.GetX(), p.GetY()
			}
			s.Orders = append(s.Orders, o)
		}
		f.Units = append(f.Units, s)
	}
	return f
}
