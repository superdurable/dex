---
section: Added
---
- **ClientOnlyFlow** marks a Flow that only a Client uses: it types the RPCs and Streams of a Flow type that Workers of another application or language serve, so **Client.InvokeRPC** and **Client.ReadStream** can call them. **NewRegistry** rejects a client-only Flow that declares Steps, **NewWorker** returns **ClientOnlyFlowError** for a Registry that holds one, and **IsClientOnlyFlow** reports the marker. [API reference](pathname:///api/go/latest/dex/#type-clientonlyflow).
