package model

type SupportAssetRequest struct {
	ConsumerToken string `json:"consumer_token"`
}

type SupportAsset struct {
	Guid        string `json:"guid"`
	AssetName   string `json:"asset_name"`
	AssetSymbol string `json:"asset_symbol"`
	AssetLogo   string `json:"asset_logo"`
}

type SupportAssetResponse struct {
	Code    uint64         `json:"code"`
	Message string         `json:"message"`
	Result  []SupportAsset `json:"result"`
}
