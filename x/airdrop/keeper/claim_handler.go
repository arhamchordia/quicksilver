package keeper

import (
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	gov "github.com/cosmos/cosmos-sdk/x/gov/types"

	"github.com/ingenuity-build/quicksilver/utils"
	"github.com/ingenuity-build/quicksilver/x/airdrop/types"
	icstypes "github.com/ingenuity-build/quicksilver/x/interchainstaking/types"
	osmosislockuptypes "github.com/osmosis-labs/osmosis/v9/x/lockup/types"

	participationrewardskeeper "github.com/ingenuity-build/quicksilver/x/participationrewards/keeper"
	participationrewardstypes "github.com/ingenuity-build/quicksilver/x/participationrewards/types"
)

var (
	tier1 = "0.05"
	tier2 = "0.10"
	tier3 = "0.15"
	tier4 = "0.22"
	tier5 = "0.30"
)

func (k Keeper) HandleClaim(ctx sdk.Context, cr types.ClaimRecord, action types.Action, proofs []*types.Proof) (uint64, error) {
	// action already completed, nothing to claim
	if _, exists := cr.ActionsCompleted[int32(action)]; exists {
		return 0, fmt.Errorf("%s already completed", types.Action_name[int32(action)])
	}

	switch action {
	case types.ActionInitialClaim:
		return 0, nil
	case types.ActionDepositT1:
		return k.handleDeposit(ctx, &cr, action, sdk.MustNewDecFromStr(tier1))
	case types.ActionDepositT2:
		return k.handleDeposit(ctx, &cr, action, sdk.MustNewDecFromStr(tier2))
	case types.ActionDepositT3:
		return k.handleDeposit(ctx, &cr, action, sdk.MustNewDecFromStr(tier3))
	case types.ActionDepositT4:
		return k.handleDeposit(ctx, &cr, action, sdk.MustNewDecFromStr(tier4))
	case types.ActionDepositT5:
		return k.handleDeposit(ctx, &cr, action, sdk.MustNewDecFromStr(tier5))
	case types.ActionStakeQCK:
		return k.handleBondedDelegation(ctx, &cr, action)
	case types.ActionSignalIntent:
		return k.handleZoneIntent(ctx, &cr, action)
	case types.ActionQSGov:
		return k.handleGovernanceParticipation(ctx, &cr, action)
	case types.ActionGbP:
		// TODO: implement handler once GbP is implemented
	case types.ActionOsmosis:
		return k.handleOsmosisLP(ctx, &cr, action, proofs)
	default:
		return 0, fmt.Errorf("undefined action [%d]", action)
	}

	return 0, fmt.Errorf("handler not implemented for [%d] %s", action, types.Action_name[int32(action)])
}

// ------------
// # Handlers #
// ------------

// handleDeposit
func (k Keeper) handleDeposit(ctx sdk.Context, cr *types.ClaimRecord, action types.Action, threshold sdk.Dec) (uint64, error) {
	if err := k.verifyDeposit(ctx, *cr, threshold); err != nil {
		return 0, err
	}

	return k.completeClaim(ctx, cr, action)
}

// handleBondedDelegation
func (k Keeper) handleBondedDelegation(ctx sdk.Context, cr *types.ClaimRecord, action types.Action) (uint64, error) {
	if err := k.verifyBondedDelegation(ctx, cr.Address); err != nil {
		return 0, err
	}

	return k.completeClaim(ctx, cr, action)
}

// handleZoneIntent
func (k Keeper) handleZoneIntent(ctx sdk.Context, cr *types.ClaimRecord, action types.Action) (uint64, error) {
	if err := k.verifyZoneIntent(ctx, cr.ChainId, cr.Address); err != nil {
		return 0, err
	}

	return k.completeClaim(ctx, cr, action)
}

// handleZoneIntent
func (k Keeper) handleGovernanceParticipation(ctx sdk.Context, cr *types.ClaimRecord, action types.Action) (uint64, error) {
	if err := k.verifyGovernanceParticipation(ctx, cr.Address); err != nil {
		return 0, err
	}

	return k.completeClaim(ctx, cr, action)
}

// handleOsmosisLP
func (k Keeper) handleOsmosisLP(ctx sdk.Context, cr *types.ClaimRecord, action types.Action, proofs []*types.Proof) (uint64, error) {
	if len(proofs) == 0 {
		return 0, fmt.Errorf("expects at least one LP proof")
	}
	if err := k.verifyOsmosisLP(ctx, proofs, *cr); err != nil {
		return 0, err
	}

	return k.completeClaim(ctx, cr, action)
}

// -------------
// # Verifiers #
// -------------

// verifyDeposit
func (k Keeper) verifyDeposit(ctx sdk.Context, cr types.ClaimRecord, threshold sdk.Dec) error {
	addr, err := sdk.AccAddressFromBech32(cr.Address)
	if err != nil {
		return err
	}

	zone, ok := k.icsKeeper.GetZone(ctx, cr.ChainId)
	if !ok {
		return fmt.Errorf("zone not found for %s", cr.ChainId)
	}

	// obtain all deposit receipts for this user on this zone
	rcpts, err := k.icsKeeper.UserZoneReceipts(ctx, &zone, addr)
	if err != nil {
		return fmt.Errorf("unable to obtain zone receipts for %s on zone %s: %w", cr.Address, cr.ChainId, err)
	}

	// sum gross deposits amount
	gdAmount := sdk.NewInt(0)
	for _, rcpt := range rcpts {
		gdAmount = gdAmount.Add(rcpt.Amount.AmountOf(zone.BaseDenom))
	}

	// calculate target amount
	tAmount := threshold.MulInt64(int64(cr.BaseValue)).TruncateInt()

	if gdAmount.LT(tAmount) {
		return fmt.Errorf("insufficient deposit amount")
	}

	return nil
}

// verifyBondedDelegation indicates if the given address has an active bonded
// delegation of QCK on the Quicksilver zone.
func (k Keeper) verifyBondedDelegation(ctx sdk.Context, address string) error {
	addr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return err
	}

	amount := k.stakingKeeper.GetDelegatorBonded(ctx, addr)
	if !amount.IsPositive() {
		return fmt.Errorf("ActionStakeQCK: no bonded delegation")
	}
	return nil
}

// verifyZoneIntent indicates if the given address has intent set for the given
// zone (chainID).
func (k Keeper) verifyZoneIntent(ctx sdk.Context, chainID string, address string) error {
	addr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return err
	}

	zone, ok := k.icsKeeper.GetZone(ctx, chainID)
	if !ok {
		return fmt.Errorf("zone %s not found", chainID)
	}

	intent, ok := k.icsKeeper.GetIntent(ctx, zone, addr.String(), false)
	if !ok || len(intent.Intents) == 0 {
		return fmt.Errorf("intent not found or no intents set for %s", addr)
	}

	return nil
}

// verifyGovernanceParticipation indicates if the given address has voted on
// any governance proposals on the Quicksilver zone.
func (k Keeper) verifyGovernanceParticipation(ctx sdk.Context, address string) error {
	addr, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return err
	}

	voted := false
	k.govKeeper.IterateProposals(ctx, func(proposal gov.Proposal) (stop bool) {
		_, found := k.govKeeper.GetVote(ctx, proposal.ProposalId, addr)
		if found {
			voted = true
			return true
		}
		return false
	})

	if !voted {
		return fmt.Errorf("no governance votes by %s", addr)
	}

	return nil
}

// verifyOsmosisLP utilizes cross-chain-verification (XCV) to indicate if the
// given address provides any liquidity of the zones qAssets on the Osmosis
// chain.
//
// It utilizes Osmosis query:
//
//	rpc LockedByID(LockedRequest) returns (LockedResponse);
func (k Keeper) verifyOsmosisLP(ctx sdk.Context, proofs []*types.Proof, cr types.ClaimRecord) error {
	// get Osmosis zone
	var osmoZone *icstypes.Zone
	k.icsKeeper.IterateZones(ctx, func(_ int64, zone icstypes.Zone) (stop bool) {
		if zone.AccountPrefix == "osmo" {
			osmoZone = &zone
			return true
		}
		return false
	})
	if osmoZone == nil {
		return fmt.Errorf("unable to find Osmosis zone")
	}

	uAmount := sdk.ZeroInt()
	dupCheck := make(map[string]struct{})
	for i, p := range proofs {
		proof := p

		// check for duplicate proof submission
		if _, exists := dupCheck[string(proof.Key)]; exists {
			return fmt.Errorf("duplicate proof submitted, %s", proof.Key)
		}
		dupCheck[string(proof.Key)] = struct{}{}

		// validate proof tx
		if err := utils.ValidateProofOps(
			ctx,
			&k.icsKeeper.IBCKeeper,
			osmoZone.ConnectionId,
			osmoZone.ChainId,
			proof.Height,
			"lockup",
			proof.Key,
			proof.Data,
			proof.ProofOps,
		); err != nil {
			return fmt.Errorf("proofs [%d]: %w", i, err)
		}

		var lockedResp osmosislockuptypes.LockedResponse
		k.cdc.MustUnmarshal(proof.Data, &lockedResp)

		// verify proof lock owner address is claim record address
		if lockedResp.Lock.Owner != cr.Address {
			return fmt.Errorf("invalid lock owner, expected %s got %s", cr.Address, lockedResp.Lock.Owner)
		}

		// verify pool is for the relevant zone
		// and sum user amounts
		amount, err := k.verifyPoolAndGetAmount(ctx, lockedResp, cr)
		if err != nil {
			return err
		}
		uAmount = uAmount.Add(amount)
	}

	// calculate target amount
	dThreshold := sdk.MustNewDecFromStr(tier4)
	if err := k.verifyDeposit(ctx, cr, dThreshold); err != nil {
		return fmt.Errorf("%w, must reach at least %s of %d", err, tier4, cr.BaseValue)
	}
	tAmount := dThreshold.MulInt64(int64(cr.BaseValue / 2)).TruncateInt()

	// check liquidity threshold
	if uAmount.LT(tAmount) {
		return fmt.Errorf("insufficient liquidity, expects at least %d, got %d", tAmount, uAmount)
	}

	return nil
}

func (k Keeper) verifyPoolAndGetAmount(ctx sdk.Context, lockedResp osmosislockuptypes.LockedResponse, cr types.ClaimRecord) (sdk.Int, error) {
	gammdenom := lockedResp.Lock.Coins.GetDenomByIndex(0)
	poolID := "osmosis/pool" + gammdenom[strings.LastIndex(gammdenom, "/"):]
	pd, ok := k.prKeeper.GetProtocolData(ctx, poolID)
	if !ok {
		return sdk.ZeroInt(), fmt.Errorf("unable to obtain protocol data for %s", poolID)
	}

	ipool, err := participationrewardskeeper.UnmarshalProtocolData("osmosispool", pd.Data)
	if err != nil {
		return sdk.ZeroInt(), err
	}
	pool, _ := ipool.(participationrewardstypes.OsmosisPoolProtocolData)

	poolDenom := ""
	for zk, zd := range pool.Zones {
		if zk == cr.ChainId {
			poolDenom = zd
			break
		}
	}

	if poolDenom == "" {
		return sdk.ZeroInt(), fmt.Errorf("invalid zone, pool zone must match %s", cr.ChainId)
	}

	// calculate user gamm ratio and LP asset amount
	ugamm := lockedResp.Lock.Coins.AmountOf(gammdenom) // user's gamm amount
	pgamm := pool.PoolData.GetTotalShares()            // total pool gamm amount
	if pgamm.IsZero() {
		return sdk.ZeroInt(), fmt.Errorf("empty pool, %s", poolID)
	}
	uratio := sdk.NewDecFromInt(ugamm).QuoInt(pgamm)

	zasset := pool.PoolData.GetTotalPoolLiquidity(ctx).AmountOf(poolDenom) // pool zone asset amount
	uAmount := uratio.MulInt(zasset).TruncateInt()

	return uAmount, nil
}

// -----------
// # Helpers #
// -----------

func (k Keeper) completeClaim(ctx sdk.Context, cr *types.ClaimRecord, action types.Action) (uint64, error) {
	// update ClaimRecord and obtain total claim amount
	claimAmount, err := k.getClaimAmountAndUpdateRecord(ctx, cr, action)
	if err != nil {
		return 0, err
	}

	// send coins to address
	coins, err := k.sendCoins(ctx, *cr, claimAmount)
	if err != nil {
		return 0, err
	}

	// emit events
	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventTypeClaim,
			sdk.NewAttribute(sdk.AttributeKeySender, cr.Address),
			sdk.NewAttribute("zone", cr.ChainId),
			sdk.NewAttribute(sdk.AttributeKeyAction, action.String()),
			sdk.NewAttribute(sdk.AttributeKeyAmount, coins.String()),
		),
	})

	return claimAmount, nil
}

// getClaimAmountAndUpdateRecord calculates and returns the claimable amount
// after updating the relevant claim record.
func (k Keeper) getClaimAmountAndUpdateRecord(ctx sdk.Context, cr *types.ClaimRecord, action types.Action) (uint64, error) {
	var claimAmount uint64

	// The concept here is to intuitively claim all outstanding deposit tiers
	// that are below the current deposit claim (improved user experience).
	if action > types.ActionDepositT1 && action <= types.ActionDepositT5 {
		for a := types.ActionDepositT1; a <= action; a++ {
			if _, exists := cr.ActionsCompleted[int32(a)]; !exists {
				// obtain claimable amount per deposit action
				claimable, err := k.GetClaimableAmountForAction(ctx, cr.ChainId, cr.Address, a)
				if err != nil {
					return 0, err
				}

				// update claim record
				cr.ActionsCompleted[int32(a)] = &types.CompletedAction{
					CompleteTime: ctx.BlockTime(),
					ClaimAmount:  claimable,
				}

				// sum total claimable
				claimAmount += claimable
			}
		}
	} else {
		// obtain claimable amount
		claimable, err := k.GetClaimableAmountForAction(ctx, cr.ChainId, cr.Address, action)
		if err != nil {
			return 0, err
		}

		// set claim amount
		claimAmount = claimable

		// update claim record
		cr.ActionsCompleted[int32(action)] = &types.CompletedAction{
			CompleteTime: ctx.BlockTime(),
			ClaimAmount:  claimAmount,
		}
	}

	// set claim record
	if err := k.SetClaimRecord(ctx, *cr); err != nil {
		return 0, err
	}

	return claimAmount, nil
}

func (k Keeper) sendCoins(ctx sdk.Context, cr types.ClaimRecord, amount uint64) (sdk.Coins, error) {
	coins := sdk.NewCoins(
		sdk.NewCoin(k.stakingKeeper.BondDenom(ctx), sdk.NewIntFromUint64(amount)),
	)

	addr, err := sdk.AccAddressFromBech32(cr.Address)
	if err != nil {
		return sdk.NewCoins(), err
	}

	zoneDropAccount := types.ModuleName + "." + cr.ChainId
	if err = k.bankKeeper.SendCoinsFromModuleToAccount(ctx, zoneDropAccount, addr, coins); err != nil {
		return sdk.NewCoins(), err
	}

	return coins, nil
}
