use block2::DynBlock;
use objc2::{
    DefinedClass, MainThreadMarker, MainThreadOnly, define_class,
    ffi::{OBJC_ASSOCIATION_RETAIN_NONATOMIC, objc_setAssociatedObject},
    msg_send,
    rc::{Retained, Weak},
    runtime::{AnyObject, NSObject, NSObjectProtocol, ProtocolObject, Sel},
};
use objc2_web_kit::{
    WKNavigationAction, WKNavigationActionPolicy, WKNavigationDelegate, WKWebView,
};
use std::sync::atomic::Ordering;
use tauri::Webview;
use url::Url;

// Wry's macOS navigation callback carries only a URL, even for a subframe.
// Keep Wry in charge of top-level navigation, downloads and lifecycle events.
type Delegate = ProtocolObject<dyn WKNavigationDelegate>;
static DELEGATE_KEY: u8 = 0;

define_class!(
    #[unsafe(super(NSObject))]
    #[thread_kind = MainThreadOnly]
    #[ivars = Weak<Delegate>]
    struct WorkbenchFrameDelegate;

    unsafe impl NSObjectProtocol for WorkbenchFrameDelegate {
        #[unsafe(method(respondsToSelector:))]
        fn responds_to_selector(&self, selector: Sel) -> bool {
            let own: bool = unsafe { msg_send![super(self), respondsToSelector: selector] };
            own || self.ivars().load().is_some_and(|delegate| delegate.respondsToSelector(selector))
        }
    }

    impl WorkbenchFrameDelegate {
        #[unsafe(method(forwardingTargetForSelector:))]
        fn forwarding_target(&self, selector: Sel) -> *const AnyObject {
            self.ivars().load()
                .filter(|delegate| delegate.respondsToSelector(selector))
                .map_or(std::ptr::null(), |delegate| Retained::as_ptr(&delegate).cast())
        }
    }

    unsafe impl WKNavigationDelegate for WorkbenchFrameDelegate {
        #[unsafe(method(webView:decidePolicyForNavigationAction:decisionHandler:))]
        fn decide_navigation(
            &self,
            webview: &WKWebView,
            action: &WKNavigationAction,
            reply: &DynBlock<dyn Fn(WKNavigationActionPolicy)>,
        ) {
            // These objects are supplied by WebKit on the main thread.
            unsafe {
                if action.targetFrame().is_some_and(|frame| !frame.isMainFrame()) {
                    let permitted = !super::WORKBENCH_FROZEN.load(Ordering::SeqCst)
                        && action.request().URL().and_then(|url| url.absoluteString())
                            .is_some_and(|url| permitted_frame(&url.to_string()));
                    reply.call((if permitted {
                        WKNavigationActionPolicy::Allow
                    } else {
                        WKNavigationActionPolicy::Cancel
                    },));
                } else if let Some(delegate) = self.ivars().load() {
                    delegate.webView_decidePolicyForNavigationAction_decisionHandler(webview, action, reply);
                } else {
                    reply.call((WKNavigationActionPolicy::Cancel,));
                }
            }
        }
    }
);

fn permitted_frame(target: &str) -> bool {
    target == "about:blank"
        || Url::parse(target).is_ok_and(|url| {
            url.scheme() == "http"
                && url.host_str() == Some("127.0.0.1")
                && url.port().is_some()
                && url.username().is_empty()
                && url.password().is_none()
        })
}

pub(super) fn install(view: &Webview) -> tauri::Result<()> {
    view.with_webview(|platform| {
        let mtm = MainThreadMarker::new().expect("Tauri with_webview runs on the main thread");
        // Tauri supplies a live WKWebView for this callback. WebKit's delegate
        // property is weak, so retain the adapter on the WKWebView itself. Wry
        // separately retains its original delegate for the same view lifetime.
        unsafe {
            let webview = &*platform.inner().cast::<WKWebView>();
            if let Some(original) = webview.navigationDelegate() {
                let adapter =
                    WorkbenchFrameDelegate::alloc(mtm).set_ivars(Weak::from_retained(&original));
                let adapter: Retained<WorkbenchFrameDelegate> = msg_send![super(adapter), init];
                objc_setAssociatedObject(
                    platform.inner().cast(),
                    (&raw const DELEGATE_KEY).cast(),
                    Retained::as_ptr(&adapter).cast_mut().cast(),
                    OBJC_ASSOCIATION_RETAIN_NONATOMIC,
                );
                webview.setNavigationDelegate(Some(ProtocolObject::from_ref(&*adapter)));
            }
        }
    })
}
