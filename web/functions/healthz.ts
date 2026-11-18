import { proxyAPI, type ProxyContext } from "../edge/proxy";

export const onRequest = (context: ProxyContext) => proxyAPI(context);
