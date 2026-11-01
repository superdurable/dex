package io.superdurable.gen;

import static io.grpc.MethodDescriptor.generateFullMethodName;

/**
 * <pre>
 * Hosted by server-zd; operators call these RPCs through dexcli.
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
  private static volatile io.grpc.MethodDescriptor<io.superdurable.gen.StepDownActiveNodeRequest,
      io.superdurable.gen.StepDownActiveNodeResponse> getStepDownActiveNodeMethod;

  @io.grpc.stub.annotations.RpcMethod(
      fullMethodName = SERVICE_NAME + '/' + "StepDownActiveNode",
      requestType = io.superdurable.gen.StepDownActiveNodeRequest.class,
      responseType = io.superdurable.gen.StepDownActiveNodeResponse.class,
      methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
  public static io.grpc.MethodDescriptor<io.superdurable.gen.StepDownActiveNodeRequest,
      io.superdurable.gen.StepDownActiveNodeResponse> getStepDownActiveNodeMethod() {
    io.grpc.MethodDescriptor<io.superdurable.gen.StepDownActiveNodeRequest, io.superdurable.gen.StepDownActiveNodeResponse> getStepDownActiveNodeMethod;
    if ((getStepDownActiveNodeMethod = AdminServiceGrpc.getStepDownActiveNodeMethod) == null) {
      synchronized (AdminServiceGrpc.class) {
        if ((getStepDownActiveNodeMethod = AdminServiceGrpc.getStepDownActiveNodeMethod) == null) {
          AdminServiceGrpc.getStepDownActiveNodeMethod = getStepDownActiveNodeMethod =
              io.grpc.MethodDescriptor.<io.superdurable.gen.StepDownActiveNodeRequest, io.superdurable.gen.StepDownActiveNodeResponse>newBuilder()
              .setType(io.grpc.MethodDescriptor.MethodType.UNARY)
              .setFullMethodName(generateFullMethodName(SERVICE_NAME, "StepDownActiveNode"))
              .setSampledToLocalTracing(true)
              .setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  io.superdurable.gen.StepDownActiveNodeRequest.getDefaultInstance()))
              .setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(
                  io.superdurable.gen.StepDownActiveNodeResponse.getDefaultInstance()))
              .setSchemaDescriptor(new AdminServiceMethodDescriptorSupplier("StepDownActiveNode"))
              .build();
        }
      }
    }
    return getStepDownActiveNodeMethod;
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
   * Hosted by server-zd; operators call these RPCs through dexcli.
   * </pre>
   */
  public interface AsyncService {

    /**
     * <pre>
     * Makes the active Server process of a node ID give up its node lease.
     * The active process stops serving and waits, within a bound, for in-flight
     * commits. It then releases the node lease and stays running as a standby
     * process. It waits for a hold-off before it competes for the lease again, so
     * another standby process of the same node ID can take over first. If none
     * does, it takes the lease back. The call returns after the release.
     * Each call steps down whichever process is active when the call arrives.
     * A blind retry can therefore step down the process that just took over.
     * </pre>
     */
    default void stepDownActiveNode(io.superdurable.gen.StepDownActiveNodeRequest request,
        io.grpc.stub.StreamObserver<io.superdurable.gen.StepDownActiveNodeResponse> responseObserver) {
      io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getStepDownActiveNodeMethod(), responseObserver);
    }
  }

  /**
   * Base class for the server implementation of the service AdminService.
   * <pre>
   * Hosted by server-zd; operators call these RPCs through dexcli.
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
   * Hosted by server-zd; operators call these RPCs through dexcli.
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
     * Makes the active Server process of a node ID give up its node lease.
     * The active process stops serving and waits, within a bound, for in-flight
     * commits. It then releases the node lease and stays running as a standby
     * process. It waits for a hold-off before it competes for the lease again, so
     * another standby process of the same node ID can take over first. If none
     * does, it takes the lease back. The call returns after the release.
     * Each call steps down whichever process is active when the call arrives.
     * A blind retry can therefore step down the process that just took over.
     * </pre>
     */
    public void stepDownActiveNode(io.superdurable.gen.StepDownActiveNodeRequest request,
        io.grpc.stub.StreamObserver<io.superdurable.gen.StepDownActiveNodeResponse> responseObserver) {
      io.grpc.stub.ClientCalls.asyncUnaryCall(
          getChannel().newCall(getStepDownActiveNodeMethod(), getCallOptions()), request, responseObserver);
    }
  }

  /**
   * A stub to allow clients to do synchronous rpc calls to service AdminService.
   * <pre>
   * Hosted by server-zd; operators call these RPCs through dexcli.
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
     * Makes the active Server process of a node ID give up its node lease.
     * The active process stops serving and waits, within a bound, for in-flight
     * commits. It then releases the node lease and stays running as a standby
     * process. It waits for a hold-off before it competes for the lease again, so
     * another standby process of the same node ID can take over first. If none
     * does, it takes the lease back. The call returns after the release.
     * Each call steps down whichever process is active when the call arrives.
     * A blind retry can therefore step down the process that just took over.
     * </pre>
     */
    public io.superdurable.gen.StepDownActiveNodeResponse stepDownActiveNode(io.superdurable.gen.StepDownActiveNodeRequest request) {
      return io.grpc.stub.ClientCalls.blockingUnaryCall(
          getChannel(), getStepDownActiveNodeMethod(), getCallOptions(), request);
    }
  }

  /**
   * A stub to allow clients to do ListenableFuture-style rpc calls to service AdminService.
   * <pre>
   * Hosted by server-zd; operators call these RPCs through dexcli.
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
     * Makes the active Server process of a node ID give up its node lease.
     * The active process stops serving and waits, within a bound, for in-flight
     * commits. It then releases the node lease and stays running as a standby
     * process. It waits for a hold-off before it competes for the lease again, so
     * another standby process of the same node ID can take over first. If none
     * does, it takes the lease back. The call returns after the release.
     * Each call steps down whichever process is active when the call arrives.
     * A blind retry can therefore step down the process that just took over.
     * </pre>
     */
    public com.google.common.util.concurrent.ListenableFuture<io.superdurable.gen.StepDownActiveNodeResponse> stepDownActiveNode(
        io.superdurable.gen.StepDownActiveNodeRequest request) {
      return io.grpc.stub.ClientCalls.futureUnaryCall(
          getChannel().newCall(getStepDownActiveNodeMethod(), getCallOptions()), request);
    }
  }

  private static final int METHODID_STEP_DOWN_ACTIVE_NODE = 0;

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
        case METHODID_STEP_DOWN_ACTIVE_NODE:
          serviceImpl.stepDownActiveNode((io.superdurable.gen.StepDownActiveNodeRequest) request,
              (io.grpc.stub.StreamObserver<io.superdurable.gen.StepDownActiveNodeResponse>) responseObserver);
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
          getStepDownActiveNodeMethod(),
          io.grpc.stub.ServerCalls.asyncUnaryCall(
            new MethodHandlers<
              io.superdurable.gen.StepDownActiveNodeRequest,
              io.superdurable.gen.StepDownActiveNodeResponse>(
                service, METHODID_STEP_DOWN_ACTIVE_NODE)))
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
              .addMethod(getStepDownActiveNodeMethod())
              .build();
        }
      }
    }
    return result;
  }
}
