/**
 * Purpose: Jest global test setup — polyfills missing from jsdom.
 * Inputs:  Loaded by jest setupFilesAfterEnv before each test suite
 * Outputs: Extended DOM environment for component tests
 * Constraints: jsdom does not implement scrollIntoView or ResizeObserver
 * SPORT: T-E1-07
 */

import "@testing-library/jest-dom";

// jsdom does not implement scrollIntoView — mock it globally so components
// that call el.scrollIntoView() do not throw in the test environment.
window.HTMLElement.prototype.scrollIntoView = jest.fn();

// ResizeObserver is used by some UI components; jsdom lacks it.
global.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
};
