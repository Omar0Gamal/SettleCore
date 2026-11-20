import React from "react";
import { Composition } from "remotion";
import { SettleCoreLaunch } from "./SettleCoreLaunch";

export const SETTLECORE_FPS = 60;
export const SETTLECORE_WIDTH = 1920;
export const SETTLECORE_HEIGHT = 1080;
export const SETTLECORE_DURATION_FRAMES = 3000;

export const RemotionRoot: React.FC = () => {
  return (
    <Composition
      id="SettleCoreLaunch"
      component={SettleCoreLaunch}
      durationInFrames={SETTLECORE_DURATION_FRAMES}
      fps={SETTLECORE_FPS}
      width={SETTLECORE_WIDTH}
      height={SETTLECORE_HEIGHT}
    />
  );
};
