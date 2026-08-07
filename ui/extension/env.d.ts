// Minimal ambient typing for the build-time NODE_ENV constant that webpack's
// DefinePlugin inlines. Declared narrowly here so the browser bundle's type space
// doesn't pull in all of @types/node.
declare const process: {
  env: {
    NODE_ENV?: 'development' | 'production' | 'test';
  };
};
