import Rysk, { Env } from "ryskv12";
import { isJSONRPCResponse, isRequest } from "ryskv12/models";

const main = () => {
  const pk = process.env.RYSK_SDK_PK || "...";
  const maker = "0x...";
  const sdk = new Rysk(Env.TESTNET, pk, "./ryskV12");

  const makerChan = "MAKER_CHAN";
  const makerProc = sdk.execute(sdk.connectArgs(makerChan, "maker"));
  makerProc.on("open", () => {
    console.log("connected");
  });
  makerProc.onmessage = (d) => {
    console.log(d.toString());
  };
  makerProc.on("error", (d) => {
    console.log(d.toString());
  });

  const rfqHandler = (payload) => {
    try {
      const data = JSON.parse(payload.toString());
      if (isJSONRPCResponse(data)) {
        const { id, result } = data;
        if (isRequest(result)) {
          // replace with actual quoting logic
          const quote = {
            assetAddress: result.asset,
            chainId: result.chainId,
            expiry: result.expiry,
            isPut: result.isPut,
            isTakerBuy: false,
            maker,
            nonce: Date.now().toString(),
            price: "1000000000",
            quantity: result.quantity,
            strike: result.strike,
            validUntil: Math.ceil(Date.now() / 1000 + 30),
            usd: result.usd,
            collateralAsset: result.collateralAsset,
          };
          let proc = sdk.execute(sdk.quoteArgs(makerChan, id, quote));

          proc.on("message", (d) => {
            console.log(d.toString());
          });
          proc.on("error", (d) => {
            console.log(d.toString());
          });
        }
      }
    } catch (e) {
      console.log("error");
      console.error(e);
    }
  };

  const rfqChan = "HYPE_CHAN";
  const p = sdk.execute(
    sdk.connectArgs(rfqChan, "rfqs/0x5555555555555555555555555555555555555555")
  );

  p.onmessage = rfqHandler;
};

main();
