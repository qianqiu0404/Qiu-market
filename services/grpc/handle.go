package grpc

import (
	"context"

	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/services/grpc/proto"
)

func (ms *MarketRpcService) GetSupportAsset(ctx context.Context, in *proto.SupportAssetRequest) (*proto.SupportAssetResponse, error) {
	assetList, err := ms.db.Asset.QueryAssets()
	if err != nil {
		log.Error("get support asset fail", "error", err)
		return &proto.SupportAssetResponse{
			ReturnCode: 4000,
			Message:    "get support asset fail",
		}, nil
	}
	var returnAssetList []*proto.AssetItem
	for _, asset := range assetList {
		assetItem := proto.AssetItem{
			Guid:        asset.Guid,
			AssetName:   asset.AssetName,
			AssetLogo:   asset.AssetLogo,
			AssetSymbol: asset.AssetSymbol,
		}
		returnAssetList = append(returnAssetList, &assetItem)
	}
	return &proto.SupportAssetResponse{
		ReturnCode: 2000,
		Message:    "get support asset success",
		Asset:      returnAssetList,
	}, nil
}
