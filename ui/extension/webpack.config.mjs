import path from 'path';
import { codecovWebpackPlugin } from '@codecov/webpack-plugin';

export default (_env, argv) => ({
  entry: './index.tsx',
  output: {
    filename: 'extension-promoter.js',
    // Async chunks (e.g. the dev-only mock fixture) must also match Argo CD's
    // ^extension(.*)\.js$ scan so the extension installer serves them.
    chunkFilename: 'extension-promoter-[name].js',
    path: path.resolve(process.cwd(), 'dist'),
    library: { type: 'window' },
  },
  resolve: {
    extensions: ['.ts', '.tsx', '.js'],
    alias: {
      '@components-lib': path.resolve(process.cwd(), '../components-lib/src'),
      '@shared': path.resolve(process.cwd(), '../shared/src'),
    },
  },
  externals: {
    react: 'React',
    'react-dom': 'ReactDOM',
  },
  module: {
    rules: [
      {
        test: /\.tsx?$/,
        use: 'ts-loader',
        exclude: /node_modules/,
      },
      {
        test: /\.s?css$/,
        use: [
          'style-loader',
          'css-loader',
          {
            loader: 'sass-loader',
            options: {
              sassOptions: {
                // style-loader injects this CSS into a <style> tag, so a leading
                // @charset (emitted by Sass when the source has non-ASCII chars) is
                // an invalid at-rule the browser drops — taking the next rule with it.
                charset: false,
              },
            },
          },
        ],
        exclude: /node_modules/,
      },
    ],
  },
  plugins: [
    codecovWebpackPlugin({
      enableBundleAnalysis: process.env.CODECOV_TOKEN !== undefined,
      bundleName: 'argocd-ui-extension',
      uploadToken: process.env.CODECOV_TOKEN,
    }),
  ],
  // Defaults to production (release/CI builds, make build-extension); the dev/watch
  // scripts pass --mode development so the mock-data branch is retained locally.
  mode: argv.mode ?? 'production',
});
