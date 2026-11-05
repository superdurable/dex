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
        self.channel
            .call(request.map(|body| TonicBody::new(PollRequestBody::new(body, reset))))
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
    reset_signal: InvocationCancellation,
    reset: Pin<Box<dyn Future<Output = ()> + Send>>,
}

impl PollRequestBody {
    fn new(body: TonicBody, reset_signal: InvocationCancellation) -> Self {
        let reset = reset_signal.clone();
        Self {
            body,
            reset_signal,
            reset: Box::pin(async move { reset.cancelled().await }),
        }
    }

    fn reset_error() -> Status {
        Status::cancelled("Worker reset busy poll stream")
    }
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
            return Poll::Ready(Some(Err(Self::reset_error())));
        }
        let frame = Pin::new(&mut self.body).poll_frame(context);
        // A reset always precedes dropping the sender, so recheck it before reporting the end.
        let is_ending = match &frame {
            Poll::Ready(None) => true,
            Poll::Ready(Some(Ok(frame))) => frame.is_trailers() || self.body.is_end_stream(),
            Poll::Ready(Some(Err(_))) | Poll::Pending => false,
        };
        if is_ending && self.reset_signal.is_cancelled() {
            return Poll::Ready(Some(Err(Self::reset_error())));
        }
        frame
    }

    fn is_end_stream(&self) -> bool {
        self.body.is_end_stream() && !self.reset_signal.is_cancelled()
    }

    fn size_hint(&self) -> SizeHint {
        self.body.size_hint()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Simulates another thread resetting the stream and dropping the sender mid-poll.
    struct ResetWhilePolledBody {
        reset_signal: InvocationCancellation,
    }

    impl Body for ResetWhilePolledBody {
        type Data = Bytes;
        type Error = Status;

        fn poll_frame(
            self: Pin<&mut Self>,
            _: &mut Context<'_>,
        ) -> Poll<Option<Result<Frame<Bytes>, Status>>> {
            self.reset_signal.cancel();
            Poll::Ready(None)
        }

        fn is_end_stream(&self) -> bool {
            self.reset_signal.is_cancelled()
        }
    }

    fn poll_once(body: &mut PollRequestBody) -> Poll<Option<Result<Frame<Bytes>, Status>>> {
        let waker = std::task::Waker::noop();
        let mut context = Context::from_waker(waker);
        Pin::new(body).poll_frame(&mut context)
    }

    #[test]
    fn reset_between_checks_reports_an_error_instead_of_end_of_stream() {
        let reset_signal = InvocationCancellation::new();
        let mut body = PollRequestBody::new(
            TonicBody::new(ResetWhilePolledBody {
                reset_signal: reset_signal.clone(),
            }),
            reset_signal,
        );
        let Poll::Ready(Some(Err(status))) = poll_once(&mut body) else {
            panic!("a reset during the poll must not end the request body");
        };
        assert_eq!(status.code(), tonic::Code::Cancelled);
        assert!(!body.is_end_stream());
    }

    #[test]
    fn unreset_body_ends_cleanly() {
        let mut body = PollRequestBody::new(TonicBody::empty(), InvocationCancellation::new());
        assert!(matches!(poll_once(&mut body), Poll::Ready(None)));
        assert!(body.is_end_stream());
    }
}
