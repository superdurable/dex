// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;
use std::pin::Pin;
use std::task::{Context, Poll};

use bytes::Bytes;
use http_body::{Body, Frame, SizeHint};
use tonic::Status;
use tonic::body::Body as TonicBody;
use tonic::codegen::{Service, http};

use crate::context::InvocationCancellation;
use crate::credential::CredentialChannel;

#[derive(Clone)]
pub(super) struct PollTransport {
    channel: CredentialChannel,
    reset: InvocationCancellation,
}

impl PollTransport {
    pub(super) fn new(channel: CredentialChannel, reset: InvocationCancellation) -> Self {
        Self { channel, reset }
    }
}

impl Service<http::Request<TonicBody>> for PollTransport {
    type Response = <CredentialChannel as Service<http::Request<TonicBody>>>::Response;
    type Error = <CredentialChannel as Service<http::Request<TonicBody>>>::Error;
    type Future = <CredentialChannel as Service<http::Request<TonicBody>>>::Future;

    fn poll_ready(&mut self, context: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.channel.poll_ready(context)
    }

    fn call(&mut self, request: http::Request<TonicBody>) -> Self::Future {
        let reset = self.reset.clone();
        self.channel.call(request.map(|body| {
            TonicBody::new(PollRequestBody {
                body,
                reset: Box::pin(async move { reset.cancelled().await }),
            })
        }))
    }
}

/// Resets a poll stream when dropped, unless a clean half-close was explicitly allowed.
pub(super) struct PollStreamResetGuard {
    reset: InvocationCancellation,
    is_clean_half_close_allowed: bool,
}

impl PollStreamResetGuard {
    pub(super) fn new(reset: InvocationCancellation) -> Self {
        Self {
            reset,
            is_clean_half_close_allowed: false,
        }
    }

    pub(super) fn reset(&self) {
        self.reset.cancel();
    }

    // Only an idle slot, or a busy slot after its final output, may end with END_STREAM.
    pub(super) fn allow_clean_half_close(&mut self) {
        self.is_clean_half_close_allowed = true;
    }
}

impl Drop for PollStreamResetGuard {
    fn drop(&mut self) {
        if !self.is_clean_half_close_allowed {
            self.reset.cancel();
        }
    }
}

struct PollRequestBody {
    body: TonicBody,
    reset: Pin<Box<dyn Future<Output = ()> + Send>>,
}

impl Body for PollRequestBody {
    type Data = Bytes;
    type Error = Status;

    fn poll_frame(
        mut self: Pin<&mut Self>,
        context: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, Status>>> {
        if self.reset.as_mut().poll(context).is_ready() {
            // A body error resets HTTP/2; returning EOF would falsely report an unstarted task.
            return Poll::Ready(Some(Err(Status::cancelled(
                "Worker reset busy poll stream",
            ))));
        }
        Pin::new(&mut self.body).poll_frame(context)
    }

    fn is_end_stream(&self) -> bool {
        self.body.is_end_stream()
    }

    fn size_hint(&self) -> SizeHint {
        self.body.size_hint()
    }
}
