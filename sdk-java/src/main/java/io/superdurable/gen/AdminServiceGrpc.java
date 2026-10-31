package io.superdurable.gen;

import static io.grpc.MethodDescriptor.generateFullMethodName;

/**
 * <pre>
 * Administrative operations hosted by the active Server owner.
 * </pre>
 */
@javax.annotation.Generated(
    value = "by gRPC proto compiler (version 1.69.1)",
    comments = "Source: dex.proto")
@io.grpc.stub.annotations.GrpcGenerated
public final class AdminServiceGrpc {

  private AdminServiceGrpc() {}

  public static final java.lang.String SERVICE_NAME = "dex.AdminService";

  // Static method descriptors that strictly reflect the proto.
  private static volatile io.grpc.MethodDescriptor<io.superdurable.gen.RebuildSearchProjectionRequest,
      io.superdurable.gen.RebuildSearchProjectionResponse> getRebuildSearchProjectionMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "RebuildSearchProjection",
      requestType = io.superdurable.gen.RebuildSearchProjectionRequest.class,
      responseType = io.superdurable.gen.RebuildSearchProjectionResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<io.superdurable.gen.RebuildSearchProjectionRequest,
      io.superdurable.gen.RebuildSearchProjectionResponse> getRebuildSearchProjectionMethod() {
    io.grpc.MethodDescriptor<io.superdurable.gen.RebuildSearchProjectionRequest, io.superdurable.gen.RebuildSearchProjectionResponse> getRebuildSearchProjectionMethod;
    if ((getRebuildSearchProjectionMethod = AdminServiceGrpc.getRebuildSearchProjectionMethod) == null) {
      synchronized (AdminServiceGrpc.class) {
        if ((getRebuildSearchProjectionMethod = AdminServiceGrpc.getRebuildSearchProjectionMethod) == null) {
          AdminServiceGrpc.getRebuildSearchProjectionMethod = getRebuildSearchProjectionMethod =
              io.grpc.MethodDescriptor.<io.superdurable.gen.RebuildSearchProjectionRequest, io.superdurable.gen.RebuildSearchProjectionResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "RebuildSearchProjection"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  io.superdurable.gen.RebuildSearchProjectionRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  io.superdurable.gen.RebuildSearchProjectionResponse.getDefaultInstance()))
              .setSchemaDescriptor(new AdminServiceMethodDescriptorSupplier("RebuildSearchProjection"))
              .build();
        }
      }
    }
    return getRebuildSearchProjectionMethod;
  }

  private static volatile io.grpc.MethodDescriptor<com.google.protobuf.Empty,
      io.superdurable.gen.GetSearchProjectionStatusResponse> getGetSearchProjectionStatusMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "GetSearchProjectionStatus",
      requestType = com.google.protobuf.Empty.class,
      responseType = io.superdurable.gen.GetSearchProjectionStatusResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<com.google.protobuf.Empty,
      io.superdurable.gen.GetSearchProjectionStatusResponse> getGetSearchProjectionStatusMethod() {
    io.grpc.MethodDescriptor<com.google.protobuf.Empty, io.superdurable.gen.GetSearchProjectionStatusResponse> getGetSearchProjectionStatusMethod;
    if ((getGetSearchProjectionStatusMethod = AdminServiceGrpc.getGetSearchProjectionStatusMethod) == null) {
      synchronized (AdminServiceGrpc.class) {
        if ((getGetSearchProjectionStatusMethod = AdminServiceGrpc.getGetSearchProjectionStatusMethod) == null) {
          AdminServiceGrpc.getGetSearchProjectionStatusMethod = getGetSearchProjectionStatusMethod =
              io.grpc.MethodDescriptor.<com.google.protobuf.Empty, io.superdurable.gen.GetSearchProjectionStatusResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "GetSearchProjectionStatus"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  com.google.protobuf.Empty.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  io.superdurable.gen.GetSearchProjectionStatusResponse.getDefaultInstance()))
              .setSchemaDescriptor(new AdminServiceMethodDescriptorSupplier("GetSearchProjectionStatus"))
              .build();
        }
      }
    }
    return getGetSearchProjectionStatusMethod;
  }

  /**
   * Creates a new async stub that supports all call types for the service
   */
  public static AdminServiceStub newStub(io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<AdminServiceStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<AdminServiceStub>() {
        @java.lang.Override
        public AdminServiceStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new AdminServiceStub(channel, callOptions);
        }
      };
    return AdminServiceStub.newStub(factory, channel);
  }

  /**
   * Creates a new blocking-style stub that supports unary and streaming output calls on the service
   */
  public static AdminServiceBlockingStub newBlockingStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<AdminServiceBlockingStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<AdminServiceBlockingStub>() {
        @java.lang.Override
        public AdminServiceBlockingStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new AdminServiceBlockingStub(channel, callOptions);
        }
      };
    return AdminServiceBlockingStub.newStub(factory, channel);
  }

  /**
   * Creates a new ListenableFuture-style stub that supports unary calls on the service
   */
  public static AdminServiceFutureStub newFutureStub(
      io.grpc.Channel channel) {
    io.grpc.stub.AbstractStub.StubFactory<AdminServiceFutureStub> factory =
      new io.grpc.stub.AbstractStub.StubFactory<AdminServiceFutureStub>() {
        @java.lang.Override
        public AdminServiceFutureStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
          return new AdminServiceFutureStub(channel, callOptions);
        }
      };
    return AdminServiceFutureStub.newStub(factory, channel);
  }

  /**
   * <pre>
   * Administrative operations hosted by the active Server owner.
   * </pre>
   */
  public interface AsyncService {

    /**
     * <pre>
     * Schedules partition rebuilds and returns before SQL rebuilding starts.
     * </pre>
     */
    default void rebuildSearchProjection(io.superdurable.gen.RebuildSearchProjectionRequest request,
        io.grpc.stub.StreamObserver<io.superdurable.gen.RebuildSearchProjectionResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getRebuildSearchProjectionMethod(), responseObserver);
    }

    /**
     * <pre>
     * Lists partitions requiring, waiting for, or performing a manual rebuild.
     * </pre>
     */
    default void getSearchProjectionStatus(com.google.protobuf.Empty request,
        io.grpc.stub.StreamObserver<io.superdurable.gen.GetSearchProjectionStatusResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getGetSearchProjectionStatusMethod(), responseObserver);
    }
  }

  /**
   * Base class for the server implementation of the service AdminService.
   * <pre>
   * Administrative operations hosted by the active Server owner.
   * </pre>
   */
  public static abstract class AdminServiceImplBase
      implements io.grpc.BindableService, AsyncService {

    @java.lang.Override public final io.grpc.ServerServiceDefinition bindService() {
      return AdminServiceGrpc.bindService(this);
    }
  }

  /**
   * A stub to allow clients to do asynchronous rpc calls to service AdminService.
   * <pre>
   * Administrative operations hosted by the active Server owner.
   * </pre>
   */
  public static final class AdminServiceStub
      extends io.grpc.stub.AbstractAsyncStub<AdminServiceStub> {
    private AdminServiceStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected AdminServiceStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new AdminServiceStub(channel, callOptions);
    }

    /**
     * <pre>
     * Schedules partition rebuilds and returns before SQL rebuilding starts.
     * </pre>
     */
    public void rebuildSearchProjection(io.superdurable.gen.RebuildSearchProjectionRequest request,
        io.grpc.stub.StreamObserver<io.superdurable.gen.RebuildSearchProjectionResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getRebuildSearchProjectionMethod(), getCallOptions()), request, responseObserver);
    }

    /**
     * <pre>
     * Lists partitions requiring, waiting for, or performing a manual rebuild.
     * </pre>
     */
    public void getSearchProjectionStatus(com.google.protobuf.Empty request,
        io.grpc.stub.StreamObserver<io.superdurable.gen.GetSearchProjectionStatusResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getGetSearchProjectionStatusMethod(), getCallOptions()), request, responseObserver);
    }
  }

  /**
   * A stub to allow clients to do synchronous rpc calls to service AdminService.
   * <pre>
   * Administrative operations hosted by the active Server owner.
   * </pre>
   */
  public static final class AdminServiceBlockingStub
      extends io.grpc.stub.AbstractBlockingStub<AdminServiceBlockingStub> {
    private AdminServiceBlockingStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected AdminServiceBlockingStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new AdminServiceBlockingStub(channel, callOptions);
    }

    /**
     * <pre>
     * Schedules partition rebuilds and returns before SQL rebuilding starts.
     * </pre>
     */
    public io.superdurable.gen.RebuildSearchProjectionResponse rebuildSearchProjection(io.superdurable.gen.RebuildSearchProjectionRequest request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getRebuildSearchProjectionMethod(), getCallOptions(), request);
    }

    /**
     * <pre>
     * Lists partitions requiring, waiting for, or performing a manual rebuild.
     * </pre>
     */
    public io.superdurable.gen.GetSearchProjectionStatusResponse getSearchProjectionStatus(com.google.protobuf.Empty request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getGetSearchProjectionStatusMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do ListenableFuture-style rpc calls to service AdminService.
   * <pre>
   * Administrative operations hosted by the active Server owner.
   * </pre>
   */
  public static final class AdminServiceFutureStub
      extends io.grpc.stub.AbstractFutureStub<AdminServiceFutureStub> {
    private AdminServiceFutureStub(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      super(channel, callOptions);
    }

    @java.lang.Override
    protected AdminServiceFutureStub build(
        io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
      return new AdminServiceFutureStub(channel, callOptions);
    }

    /**
     * <pre>
     * Schedules partition rebuilds and returns before SQL rebuilding starts.
     * </pre>
     */
    public com.google.common.util.concurrent.ListenableFuture<io.superdurable.gen.RebuildSearchProjectionResponse> rebuildSearchProjection(
        io.superdurable.gen.RebuildSearchProjectionRequest request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getRebuildSearchProjectionMethod(), getCallOptions()), request);
    }

    /**
     * <pre>
     * Lists partitions requiring, waiting for, or performing a manual rebuild.
     * </pre>
     */
    public com.google.common.util.concurrent.ListenableFuture<io.superdurable.gen.GetSearchProjectionStatusResponse> getSearchProjectionStatus(
        com.google.protobuf.Empty request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getGetSearchProjectionStatusMethod(), getCallOptions()), request);
    }
  }

  private static final int METHODID_REBUILD_SEARCH_PROJECTION = 0;
  private static final int METHODID_GET_SEARCH_PROJECTION_STATUS = 1;

  private static final class MethodHandlers<Req, Resp> implements
      io.grpc.stub.ServerCalls.UnaryMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ServerStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.ClientStreamingMethod<Req, Resp>,
      io.grpc.stub.ServerCalls.BidiStreamingMethod<Req, Resp> {
    private final AsyncService serviceImpl;
    private final int methodId;

    MethodHandlers(AsyncService serviceImpl, int methodId) {
      this.serviceImpl = serviceImpl;
      this.methodId = methodId;
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public void invoke(Req request, io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        case METHODID_REBUILD_SEARCH_PROJECTION:
          serviceImpl.rebuildSearchProjection((io.superdurable.gen.RebuildSearchProjectionRequest) request,
              (io.grpc.stub.StreamObserver<io.superdurable.gen.RebuildSearchProjectionResponse>) responseObserver);
          break;
        case METHODID_GET_SEARCH_PROJECTION_STATUS:
          serviceImpl.getSearchProjectionStatus((com.google.protobuf.Empty) request,
              (io.grpc.stub.StreamObserver<io.superdurable.gen.GetSearchProjectionStatusResponse>) responseObserver);
          break;
        default:
          throw new AssertionError();
      }
    }

    @java.lang.Override
    @java.lang.SuppressWarnings("unchecked")
    public io.grpc.stub.StreamObserver<Req> invoke(
        io.grpc.stub.StreamObserver<Resp> responseObserver) {
      switch (methodId) {
        default:
          throw new AssertionError();
      }
    }
  }

  public static final io.grpc.ServerServiceDefinition bindService(AsyncService service) {
    return io.grpc.ServerServiceDefinition.builder(getServiceDescriptor())
        .addMethod(
          getRebuildSearchProjectionMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              io.superdurable.gen.RebuildSearchProjectionRequest,
              io.superdurable.gen.RebuildSearchProjectionResponse>(
                service, METHODID_REBUILD_SEARCH_PROJECTION)))
        .addMethod(
          getGetSearchProjectionStatusMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              com.google.protobuf.Empty,
              io.superdurable.gen.GetSearchProjectionStatusResponse>(
                service, METHODID_GET_SEARCH_PROJECTION_STATUS)))
        .build();
  }

  private static abstract class AdminServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoFileDescriptorSupplier, io.grpc.protobuf.ProtoServiceDescriptorSupplier {
    AdminServiceBaseDescriptorSupplier() {}

    @java.lang.Override
    public com.google.protobuf.Descriptors.FileDescriptor getFileDescriptor() {
      return io.superdurable.gen.DexProto.getDescriptor();
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.ServiceDescriptor getServiceDescriptor() {
      return getFileDescriptor().findServiceByName("AdminService");
    }
  }

  private static final class AdminServiceFileDescriptorSupplier
      extends AdminServiceBaseDescriptorSupplier {
    AdminServiceFileDescriptorSupplier() {}
  }

  private static final class AdminServiceMethodDescriptorSupplier
      extends AdminServiceBaseDescriptorSupplier
      implements io.grpc.protobuf.ProtoMethodDescriptorSupplier {
    private final java.lang.String methodName;

    AdminServiceMethodDescriptorSupplier(java.lang.String methodName) {
      this.methodName = methodName;
    }

    @java.lang.Override
    public com.google.protobuf.Descriptors.MethodDescriptor getMethodDescriptor() {
      return getServiceDescriptor().findMethodByName(methodName);
    }
  }

  private static volatile io.grpc.ServiceDescriptor serviceDescriptor;

  public static io.grpc.ServiceDescriptor getServiceDescriptor() {
    io.grpc.ServiceDescriptor result = serviceDescriptor;
    if (result == null) {
      synchronized (AdminServiceGrpc.class) {
        result = serviceDescriptor;
        if (result == null) {
          serviceDescriptor = result = io.grpc.ServiceDescriptor.newBuilder(SERVICE_NAME)
              .setSchemaDescriptor(new AdminServiceFileDescriptorSupplier())
              .addMethod(getRebuildSearchProjectionMethod())
              .addMethod(getGetSearchProjectionStatusMethod())
              .build();
        }
      }
    }
    return result;
  }
}
