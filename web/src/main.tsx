import { render } from "preact";
import { App } from "./App";
import { PrintPage } from "./PrintPage";
import { applyTheme, deviceStorage, readTheme } from "./theme";
import "./style.css";
import "./app.css";

// tech-stack.md 2.2「路由」: the path picks the entry; the fragment carries the token.
function Root() {
  switch (location.pathname) {
    case "/print":
      return <PrintPage />;
    default:
      return <App />;
  }
}

// Before the first paint, so a light device never flashes dark.
applyTheme(readTheme(deviceStorage()));

render(<Root />, document.getElementById("app")!);
