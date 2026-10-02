import path from 'path';
import { codecovWebpackPlugin } from '@codecov/webpack-plugin';

export default {
  entry: './index.tsx',
  output: {
    filename: 'extension-promoter.js',
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
  mode: 'production',
};
