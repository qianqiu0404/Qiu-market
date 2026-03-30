package service

import (
	"github.com/ethereum/go-ethereum/log"
	"github.com/the-web3/s78-market-services/services/http/model"
)

func (h HandleSvc) GetSupportAsset(request *model.SupportAssetRequest) (*model.SupportAssetResponse, error) {
	assetList, err := h.assetView.QueryAssets()
	if err != nil {
		log.Error("query assets error", "error", err)
		return nil, err
	}
	var supportAssetList []model.SupportAsset
	for _, asset := range assetList {
		supportAsset := model.SupportAsset{
			Guid:        asset.Guid,
			AssetName:   asset.AssetName,
			AssetSymbol: asset.AssetSymbol,
			AssetLogo:   asset.AssetLogo,
		}
		supportAssetList = append(supportAssetList, supportAsset)
	}
	return &model.SupportAssetResponse{
		Code:    2000,
		Message: "get support asset success",
		Result:  supportAssetList,
	}, nil
}
