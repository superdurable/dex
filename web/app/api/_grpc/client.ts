import fs from 'node:fs';
import path from 'node:path';
import * as grpc from '@grpc/grpc-js';
import * as protoLoader from '@grpc/proto-loader';

type Callback<Response> = (error: grpc.ServiceError | null, response: Response) => void;

interface FlowServiceClient extends grpc.Client {
  SearchFlows(request: Record<string, unknown>, callback: Callback<Record<string, unknown>>): void;
  GetFlowSummary(request: Record<string, unknown>, callback: Callback<Record<string, unknown>>): void;
  GetHistoryEvents(request: Record<string, unknown>, callback: Callback<Record<string, unknown>>): void;
  WaitForHistoryEvent(request: Record<string, unknown>, callback: Callback<Record<string, unknown>>): grpc.ClientUnaryCall;
  GetFlowState(request: Record<string, unknown>, callback: Callback<Record<string, unknown>>): void;
  ResetFlow(request: Record<string, unknown>, callback: Callback<Record<string, unknown>>): void;
}

type FlowServiceConstructor = new (
  address: string,
  credentials: grpc.ChannelCredentials,
  options?: grpc.ClientOptions,
) => FlowServiceClient;

let cachedConstructor: FlowServiceConstructor | null = null;
let cachedClient: FlowServiceClient | null = null;

function resolveProtoPath(): string {
  const explicit = process.env.DEX_PROTO_PATH;
  if (explicit) {
    if (!fs.existsSync(explicit)) throw new Error(`DEX_PROTO_PATH does not exist: ${explicit}`);
    return explicit;
  }

  let directory = process.cwd();
  for (let depth = 0; depth < 8; depth += 1) {
    const candidate = path.join(directory, 'protos', 'dex.proto');
    if (fs.existsSync(candidate)) return candidate;
    const parent = path.dirname(directory);
    if (parent === directory) break;
    directory = parent;
  }
  throw new Error('Cannot locate protos/dex.proto; set DEX_PROTO_PATH');
}

function flowServiceConstructor(): FlowServiceConstructor {
  if (cachedConstructor) return cachedConstructor;
  const protoPath = resolveProtoPath();
  const definition = protoLoader.loadSync(protoPath, {
    keepCase: true,
    longs: String,
    enums: Number,
    bytes: Buffer,
    defaults: false,
    oneofs: true,
    includeDirs: [path.dirname(protoPath)],
  });
  const loaded = grpc.loadPackageDefinition(definition) as unknown as {
    dex: { FlowService: FlowServiceConstructor };
  };
  cachedConstructor = loaded.dex.FlowService;
  return cachedConstructor;
}

function flowService(): FlowServiceClient {
  if (cachedClient) return cachedClient;
  const address = process.env.DEX_SERVER_ADDRESS || '127.0.0.1:8801';
  cachedClient = new (flowServiceConstructor())(
    address,
    grpc.credentials.createInsecure(),
    {
      'grpc.max_receive_message_length': 16 * 1024 * 1024,
      'grpc.max_send_message_length': 16 * 1024 * 1024,
    },
  );
  return cachedClient;
}

function unary(
  method: keyof Pick<
    FlowServiceClient,
    | 'SearchFlows'
    | 'GetFlowSummary'
    | 'GetHistoryEvents'
    | 'GetFlowState'
    | 'ResetFlow'
  >,
  request: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  return new Promise((resolve, reject) => {
    flowService()[method](request, (error, response) => {
      if (error) {
        reject(error);
        return;
      }
      resolve(response);
    });
  });
}

export function searchFlows(request: Record<string, unknown>) {
  return unary('SearchFlows', request);
}

export function getFlowSummary(request: Record<string, unknown>) {
  return unary('GetFlowSummary', request);
}

export function getHistoryEvents(request: Record<string, unknown>) {
  return unary('GetHistoryEvents', request);
}

export function getFlowState(request: Record<string, unknown>) {
  return unary('GetFlowState', request);
}

export function resetFlow(request: Record<string, unknown>) {
  return unary('ResetFlow', request);
}

export function waitForHistoryEvent(
  request: Record<string, unknown>,
  signal: AbortSignal,
): Promise<Record<string, unknown>> {
  return new Promise((resolve, reject) => {
    const call = flowService().WaitForHistoryEvent(request, (error, response) => {
      signal.removeEventListener('abort', cancel);
      if (error) {
        reject(error);
        return;
      }
      resolve(response);
    });
    const cancel = () => call.cancel();
    signal.addEventListener('abort', cancel, { once: true });
  });
}
