//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

// Keep the layout viewport unchanged, as keyboards do on mobile browsers.
func assertModalKeyboardViewport(
	t *testing.T,
	b *packageBrowser,
	dialogSelector, scrollSelector, fieldSelector string,
) {
	t.Helper()
	b.wait(t, `document.getAnimations().every(a => a.playState !== 'running')`)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const dialog=document.querySelector(%q);
 const box=dialog.querySelector('.modal-box');
 const body=dialog.querySelector(%q);
 const field=dialog.querySelector(%q);
 const viewport=visualViewport;
 const layoutHeight=innerHeight;
 const originalHeight=box.getBoundingClientRect().height;
 try {
  Object.defineProperty(viewport, 'height', {configurable:true, value:360});
  Object.defineProperty(viewport, 'offsetTop', {configurable:true, value:40});
  viewport.dispatchEvent(new Event('resize'));
  viewport.dispatchEvent(new Event('scroll'));
  body.scrollTop=body.scrollHeight;
  field.focus();
  const bounds=box.getBoundingClientRect();
  const header=dialog.querySelector('.page-header').getBoundingClientRect();
  const input=field.getBoundingClientRect();
  if (innerHeight !== layoutHeight || bounds.top < 40 || bounds.bottom > 401 ||
   header.top < 40 || input.top < 40 || input.bottom > 401 ||
   document.activeElement !== field || body.scrollTop <= 0) return false;
 } finally {
  delete viewport.height;
  delete viewport.offsetTop;
  viewport.dispatchEvent(new Event('resize'));
  body.scrollTop=0;
 }
 return Math.abs(box.getBoundingClientRect().height-originalHeight) < 1;
})()`, dialogSelector, scrollSelector, fieldSelector))) != "true" {
		t.Fatal("modal lower field is hidden by the keyboard viewport")
	}
}

func assertDialogKeyboardBounds(
	t *testing.T,
	b *packageBrowser,
	selector string,
) {
	t.Helper()
	b.wait(t, `document.getAnimations().every(a => a.playState !== 'running')`)
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const dialog=document.querySelector(%q);
 const box=dialog.querySelector('.modal-box') || dialog;
 const controls=dialog.querySelector('.modal-action, [data-token-project-done], .page-header') ||
 dialog.querySelector(':scope > div:first-child');
 const body=dialog.querySelector('[data-modal-scroll-body], [data-token-project-list]') || box;
 const viewport=visualViewport;
 const originalHeight=box.getBoundingClientRect().height;
 try {
  Object.defineProperty(viewport, 'height', {configurable:true, value:360});
  Object.defineProperty(viewport, 'offsetTop', {configurable:true, value:0});
  viewport.dispatchEvent(new Event('resize'));
  Object.defineProperty(viewport, 'offsetTop', {configurable:true, value:40});
  viewport.dispatchEvent(new Event('scroll'));
  body.scrollTop=body.scrollHeight;
  const bounds=box.getBoundingClientRect();
  const actions=controls.getBoundingClientRect();
  if (bounds.top < 39 || bounds.bottom > 401 || actions.top < 39 ||
   actions.bottom > 401 || box.scrollWidth > box.clientWidth) return false;
  if (body.matches('[data-modal-scroll-body]') && body.clientHeight < 1) return false;
  if (dialog.matches('[data-fullscreen-dialog]')) {
   const textarea=dialog.querySelector('textarea').getBoundingClientRect();
   if (textarea.height <= 0 || textarea.bottom > 401) return false;
  }
 } finally {
  delete viewport.height;
  delete viewport.offsetTop;
  viewport.dispatchEvent(new Event('resize'));
  body.scrollTop=0;
 }
 return Math.abs(box.getBoundingClientRect().height-originalHeight) < 1;
})()`, selector))) != "true" {
		t.Fatalf(
			"dialog controls are outside the keyboard viewport: %s",
			selector,
		)
	}
}
